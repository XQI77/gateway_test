package session

import (
	"gatesvr/proto"
	"sync"
)

type NotifyBindMsgItem struct {
	NotifyData []byte

	// notify消息的类型和内容信息
	MsgType string
	Title   string
	Content string

	Metadata map[string]string

	// 绑定信息
	SyncHint proto.NotifySyncHint
	BindGrid uint32 // 绑定的client_seq_id

	// 创建时间，用于超时清理
	CreateTime int64
}

type MessageOrderingManager struct {
	// key: client_seq_id, value: notify列表
	beforeRspNotifies map[uint32][]*NotifyBindMsgItem

	// 用于存储需要在response之后下发的notify
	afterRspNotifies map[uint32][]*NotifyBindMsgItem

	mu sync.RWMutex
}

func NewMessageOrderingManager() *MessageOrderingManager {
	return &MessageOrderingManager{
		beforeRspNotifies: make(map[uint32][]*NotifyBindMsgItem),
		afterRspNotifies:  make(map[uint32][]*NotifyBindMsgItem),
	}
}

func (m *MessageOrderingManager) AddNotifyBindBeforeRsp(grid uint32, notify *NotifyBindMsgItem) bool {
	if notify == nil {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.beforeRspNotifies[grid] = append(m.beforeRspNotifies[grid], notify)
	return true
}

func (m *MessageOrderingManager) AddNotifyBindAfterRsp(grid uint32, notify *NotifyBindMsgItem) bool {
	if notify == nil {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.afterRspNotifies[grid] = append(m.afterRspNotifies[grid], notify)
	return true
}

func (m *MessageOrderingManager) GetAndRemoveBeforeNotifies(grid uint32) []*NotifyBindMsgItem {
	m.mu.Lock()
	defer m.mu.Unlock()

	notifies := m.beforeRspNotifies[grid]
	delete(m.beforeRspNotifies, grid)
	return notifies
}

func (m *MessageOrderingManager) GetAndRemoveAfterNotifies(grid uint32) []*NotifyBindMsgItem {
	m.mu.Lock()
	defer m.mu.Unlock()

	notifies := m.afterRspNotifies[grid]
	delete(m.afterRspNotifies, grid)
	return notifies
}

func (m *MessageOrderingManager) GetPendingBeforeCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, notifies := range m.beforeRspNotifies {
		count += len(notifies)
	}
	return count
}

func (m *MessageOrderingManager) GetPendingAfterCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, notifies := range m.afterRspNotifies {
		count += len(notifies)
	}
	return count
}

func (m *MessageOrderingManager) CleanupExpiredNotifies(currentTime int64, maxAge int64) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleanedCount := 0

	for grid, notifies := range m.beforeRspNotifies {
		filtered := make([]*NotifyBindMsgItem, 0, len(notifies))
		for _, notify := range notifies {
			if currentTime-notify.CreateTime <= maxAge {
				filtered = append(filtered, notify)
			} else {
				cleanedCount++
			}
		}

		if len(filtered) == 0 {
			delete(m.beforeRspNotifies, grid)
		} else {
			m.beforeRspNotifies[grid] = filtered
		}
	}

	// 清理after notifies
	for grid, notifies := range m.afterRspNotifies {
		filtered := make([]*NotifyBindMsgItem, 0, len(notifies))
		for _, notify := range notifies {
			if currentTime-notify.CreateTime <= maxAge {
				filtered = append(filtered, notify)
			} else {
				cleanedCount++
			}
		}

		if len(filtered) == 0 {
			delete(m.afterRspNotifies, grid)
		} else {
			m.afterRspNotifies[grid] = filtered
		}
	}

	return cleanedCount
}

func (m *MessageOrderingManager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.beforeRspNotifies = make(map[uint32][]*NotifyBindMsgItem)
	m.afterRspNotifies = make(map[uint32][]*NotifyBindMsgItem)
}
