package session

import (
	"container/heap"
	"fmt"
	"sync"
	"time"

	pb "gatesvr/proto"
)

type OrderedMessage struct {
	ServerSeq uint64 // 服务器序列号
	Push      *pb.ServerPush
	Data      []byte
	Timestamp time.Time
	Retries   int
	Sent      bool // 是否已发送
}

type MessageQueue []*OrderedMessage

func (mq MessageQueue) Len() int            { return len(mq) }
func (mq MessageQueue) Less(i, j int) bool  { return mq[i].ServerSeq < mq[j].ServerSeq }
func (mq MessageQueue) Swap(i, j int)       { mq[i], mq[j] = mq[j], mq[i] }
func (mq *MessageQueue) Push(x interface{}) { *mq = append(*mq, x.(*OrderedMessage)) }
func (mq *MessageQueue) Pop() interface{} {
	old := *mq
	n := len(old)
	item := old[n-1]
	*mq = old[0 : n-1]
	return item
}

const (
	MessageTimeout  = 30 * time.Second
	MaxRetries      = 3
	CleanupInterval = 10 * time.Second
)

type OrderedMessageQueue struct {
	sessionID string

	waitingQueue MessageQueue               // 等待发送的消息队列
	sentMessages map[uint64]*OrderedMessage // 已发送待确认的消息（有问题可能todo改）
	queueMux     sync.Mutex                 // 保护队列的锁

	// 序列号
	nextExpectedSeq uint64 // 下一个期望的序列号
	lastSentSeq     uint64 // 最后发送的序列号
	lastAckedSeq    uint64 // 最后确认的序列号

	maxQueueSize int

	stopped       bool
	stopCh        chan struct{}
	cleanupTicker *time.Ticker

	sendCallback func(*OrderedMessage) error
}

func NewOrderedMessageQueue(sessionID string, maxQueueSize int) *OrderedMessageQueue {
	omq := &OrderedMessageQueue{
		sessionID:       sessionID,
		waitingQueue:    make(MessageQueue, 0),
		sentMessages:    make(map[uint64]*OrderedMessage),
		nextExpectedSeq: 1,
		lastSentSeq:     0,
		lastAckedSeq:    0,
		maxQueueSize:    maxQueueSize,
		stopCh:          make(chan struct{}),
	}

	heap.Init(&omq.waitingQueue)

	omq.cleanupTicker = time.NewTicker(CleanupInterval)
	go omq.cleanupLoop()

	return omq
}

func (omq *OrderedMessageQueue) SetSendCallback(callback func(*OrderedMessage) error) {
	omq.sendCallback = callback
}

func (omq *OrderedMessageQueue) GetSendCallback() func(*OrderedMessage) error {
	return omq.sendCallback
}

func (omq *OrderedMessageQueue) EnqueueMessage(serverSeq uint64, push *pb.ServerPush, data []byte) error {
	omq.queueMux.Lock()
	defer omq.queueMux.Unlock()

	if omq.stopped {
		return fmt.Errorf("队列已停止")
	}

	if len(omq.waitingQueue) >= omq.maxQueueSize {
		return fmt.Errorf("队列已满，最大长度: %d", omq.maxQueueSize)
	}

	orderedMsg := &OrderedMessage{
		ServerSeq: serverSeq,
		Push:      push,
		Data:      data,
		Timestamp: time.Now(),
		Retries:   0,
		Sent:      false,
	}

	// 检查是否可以立即发送
	if serverSeq == omq.nextExpectedSeq {
		if err := omq.sendMessageDirectly(orderedMsg); err != nil {
			return fmt.Errorf("发送消息失败: %w", err)
		}

		// 标记为已发送并加入待确认队列
		orderedMsg.Sent = true
		omq.sentMessages[serverSeq] = orderedMsg
		omq.nextExpectedSeq++
		omq.lastSentSeq = serverSeq

		// 尝试发送队列中的后续消息
		omq.processWaitingMessages()
	} else if serverSeq > omq.nextExpectedSeq {
		// 需要等待前序消息，加入队列
		heap.Push(&omq.waitingQueue, orderedMsg)
	} else {
		// serverSeq < nextExpectedSeq，说明是重复或过期的消息
		return fmt.Errorf("消息序列号 %d 小于期望序列号 %d，忽略", serverSeq, omq.nextExpectedSeq)
	}

	return nil
}

func (omq *OrderedMessageQueue) processWaitingMessages() {
	for len(omq.waitingQueue) > 0 {
		topMsg := omq.waitingQueue[0]
		if topMsg.ServerSeq == omq.nextExpectedSeq {
			msg := heap.Pop(&omq.waitingQueue).(*OrderedMessage)
			if err := omq.sendMessageDirectly(msg); err != nil {
				heap.Push(&omq.waitingQueue, msg)
				break
			}
			msg.Sent = true
			omq.sentMessages[msg.ServerSeq] = msg
			omq.nextExpectedSeq++
			omq.lastSentSeq = msg.ServerSeq
		} else {
			break
		}
	}
}

func (omq *OrderedMessageQueue) sendMessageDirectly(msg *OrderedMessage) error {
	if omq.sendCallback != nil {
		return omq.sendCallback(msg)
	}
	return fmt.Errorf("未设置发送回调函数")
}

func (omq *OrderedMessageQueue) GetQueueStats() map[string]interface{} {
	omq.queueMux.Lock()
	defer omq.queueMux.Unlock()

	return map[string]interface{}{
		"session_id":        omq.sessionID,
		"queue_size":        len(omq.waitingQueue),
		"pending_count":     len(omq.sentMessages),
		"max_queue_size":    omq.maxQueueSize,
		"next_expected_seq": omq.nextExpectedSeq,
		"last_sent_seq":     omq.lastSentSeq,
		"last_acked_seq":    omq.lastAckedSeq,
		"stopped":           omq.stopped,
	}
}

// 重新同步序列号
func (omq *OrderedMessageQueue) ResyncSequence(clientAckSeq uint64) {
	omq.queueMux.Lock()
	defer omq.queueMux.Unlock()

	// 批量确认客户端已收到的消息
	for seqID := range omq.sentMessages {
		if seqID <= clientAckSeq {
			delete(omq.sentMessages, seqID)
		}
	}

	// 调整序列号和清理过期消息
	if clientAckSeq >= omq.nextExpectedSeq {
		omq.nextExpectedSeq = clientAckSeq + 1
		omq.lastSentSeq = clientAckSeq

		newQueue := make(MessageQueue, 0)
		for _, msg := range omq.waitingQueue {
			if msg.ServerSeq > clientAckSeq {
				newQueue = append(newQueue, msg)
			}
		}
		omq.waitingQueue = newQueue
		heap.Init(&omq.waitingQueue)
	}

	if clientAckSeq > omq.lastAckedSeq {
		omq.lastAckedSeq = clientAckSeq
	}
}

// 批量确认消息
func (omq *OrderedMessageQueue) AckMessagesUpTo(ackSeqID uint64) int {
	omq.queueMux.Lock()
	defer omq.queueMux.Unlock()

	ackedCount := 0
	for seqID := range omq.sentMessages {
		if seqID <= ackSeqID {
			delete(omq.sentMessages, seqID)
			ackedCount++
		}
	}

	if ackSeqID > omq.lastAckedSeq {
		omq.lastAckedSeq = ackSeqID
	}

	return ackedCount
}

func (omq *OrderedMessageQueue) GetPendingCount() int {
	omq.queueMux.Lock()
	defer omq.queueMux.Unlock()
	return len(omq.sentMessages)
}

func (omq *OrderedMessageQueue) cleanupLoop() {
	for {
		select {
		case <-omq.stopCh:
			return
		case <-omq.cleanupTicker.C:
			omq.cleanupExpiredMessages()
			omq.retryTimedOutMessages()
		}
	}
}

func (omq *OrderedMessageQueue) cleanupExpiredMessages() {
	omq.queueMux.Lock()
	defer omq.queueMux.Unlock()

	now := time.Now()

	// 清理待确认消息中的过期消息
	for seqID, msg := range omq.sentMessages {
		if now.Sub(msg.Timestamp) > MessageTimeout && msg.Retries >= MaxRetries {
			delete(omq.sentMessages, seqID)
		}
	}

	// 清理等待队列中的过期消息
	newQueue := make(MessageQueue, 0)
	for _, msg := range omq.waitingQueue {
		if now.Sub(msg.Timestamp) <= MessageTimeout {
			newQueue = append(newQueue, msg)
		}
	}
	omq.waitingQueue = newQueue
	heap.Init(&omq.waitingQueue)
}

func (omq *OrderedMessageQueue) retryTimedOutMessages() {
	omq.queueMux.Lock()
	defer omq.queueMux.Unlock()

	now := time.Now()
	for _, msg := range omq.sentMessages {
		if now.Sub(msg.Timestamp) > MessageTimeout/2 && msg.Retries < MaxRetries {
			if omq.sendCallback != nil && omq.sendCallback(msg) == nil {
				msg.Retries++
				msg.Timestamp = now
			}
		}
	}
}

func (omq *OrderedMessageQueue) Stop() {
	omq.queueMux.Lock()
	defer omq.queueMux.Unlock()

	if !omq.stopped {
		omq.stopped = true
		close(omq.stopCh)

		if omq.cleanupTicker != nil {
			omq.cleanupTicker.Stop()
		}

		omq.waitingQueue = make(MessageQueue, 0)
		heap.Init(&omq.waitingQueue)
		omq.sentMessages = make(map[uint64]*OrderedMessage)
	}
}
