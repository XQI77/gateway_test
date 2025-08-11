package session

import (
	"fmt"
	"gatesvr/proto"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	protobuf "google.golang.org/protobuf/proto"
)

// 会话状态枚举
type SessionState int32

const (
	SessionInited SessionState = iota // 初始化状态，等待登录
	SessionNormal                     // 正常状态，可以处理业务请求
	SessionClosed                     // 已关闭状态
)

type Session struct {
	ID           string       // 会话标识符
	Connection   *quic.Conn   // QUIC连接
	Stream       *quic.Stream // 双向流
	CreateTime   time.Time
	LastActivity time.Time

	ClientID string
	OpenID   string
	connIdx  int32
	UserIP   string

	// 会话状态
	state int32 // 状态字段
	zone  int64 // 分区标识符

	isSuccessor      bool
	isRelogin        bool
	isRedirectable   bool
	needReset        bool
	redirectableTime time.Time
	// ACK机制相关
	nextSeqID          uint64 // 下一个消息序列号
	serverSeq          uint64 // 服务器序列号
	maxClientSeq       uint64 // 最大客户端序列号
	clientAckServerSeq uint64 // 客户端确认的服务器序列号

	// 有序消息队列
	orderedQueue *OrderedMessageQueue

	// 消息保序机制 - 新增
	orderingManager *MessageOrderingManager

	closed   bool
	closeMux sync.Mutex

	closeCh chan struct{}
}

func (s *Session) NextSeqID() uint64 {
	return atomic.AddUint64(&s.nextSeqID, 1)
}

func (s *Session) UpdateActivity() {
	s.LastActivity = time.Now()
}

func (s *Session) IsExpired(timeout time.Duration) bool {
	return time.Since(s.LastActivity) > timeout
}

func (s *Session) Close() {
	s.closeMux.Lock()
	defer s.closeMux.Unlock()

	if s.closed {
		return
	}

	s.closed = true

	if s.orderedQueue != nil {
		s.orderedQueue.Stop()
	}

	s.Stream.Close()
	s.Connection.CloseWithError(0, "session closed")

	close(s.closeCh)
}

func (s *Session) IsClosed() bool {
	s.closeMux.Lock()
	defer s.closeMux.Unlock()
	return s.closed
}

func (s *Session) SetClosed() bool {
	s.closeMux.Lock()
	defer s.closeMux.Unlock()

	if s.closed {
		return true
	}

	s.closed = true
	return false
}

func (s *Session) State() SessionState {
	return SessionState(atomic.LoadInt32(&s.state))
}

func (s *Session) SetState(state SessionState) {
	atomic.StoreInt32(&s.state, int32(state))
}

func (s *Session) IsInited() bool {
	return s.State() == SessionInited
}

func (s *Session) IsNormal() bool {
	return s.State() == SessionNormal
}

func (s *Session) SetNormal() bool {
	return atomic.CompareAndSwapInt32(&s.state, int32(SessionInited), int32(SessionNormal))
}

func (s *Session) Zone() int64 {
	return atomic.LoadInt64(&s.zone)
}

func (s *Session) SetZone(zone int64) {
	atomic.StoreInt64(&s.zone, zone)
}

// 获取服务器序列号
func (s *Session) ServerSeq() uint64 {
	return atomic.LoadUint64(&s.serverSeq)
}

// 设置服务器序列号
func (s *Session) SetServerSeq(seq uint64) {
	atomic.StoreUint64(&s.serverSeq, seq)
}

// 生成新的服务器序列号
func (s *Session) NewServerSeq() uint64 {
	return atomic.AddUint64(&s.serverSeq, 1)
}

func (s *Session) MaxClientSeq() uint64 {
	return atomic.LoadUint64(&s.maxClientSeq)
}

func (s *Session) ClientAckServerSeq() uint64 {
	return atomic.LoadUint64(&s.clientAckServerSeq)
}

func (s *Session) UpdateAckServerSeq(seq uint64) error {
	atomic.StoreUint64(&s.clientAckServerSeq, seq)
	return nil
}

// IsSuccessor 检查是否为继承会话
func (s *Session) IsSuccessor() bool {
	return s.isSuccessor
}

// SetSuccessor 设置为继承会话
func (s *Session) SetSuccessor(isSuccessor bool) {
	s.isSuccessor = isSuccessor
}

// ConnIdx 获取连接索引
func (s *Session) ConnIdx() int32 {
	return s.connIdx
}

// InheritFrom 从旧会话继承数据
func (s *Session) InheritFrom(oldSession *Session) {
	// 继承关键数据
	s.SetZone(oldSession.Zone())
	s.SetServerSeq(oldSession.ServerSeq())
	s.SetSuccessor(true)

	// 继承状态
	if oldSession.IsNormal() {
		s.SetState(SessionNormal)
	}

	// 继承序列号状态
	atomic.StoreUint64(&s.clientAckServerSeq, oldSession.ClientAckServerSeq())
	atomic.StoreUint64(&s.maxClientSeq, oldSession.MaxClientSeq())

	if oldSession.orderedQueue != nil && s.orderedQueue != nil {

		s.orderedQueue.ResyncSequence(oldSession.ClientAckServerSeq())
	}
}

// 验证客户端序列号
func (s *Session) ValidateClientSeq(clientSeq uint64) bool {
	current := atomic.LoadUint64(&s.maxClientSeq)

	if current == 0 {
		atomic.StoreUint64(&s.maxClientSeq, clientSeq)
		return true
	}

	if clientSeq == current+1 {
		atomic.StoreUint64(&s.maxClientSeq, clientSeq)
		return true
	}

	return false
}

func (s *Session) ActivateSession(zone int64) bool {

	if atomic.CompareAndSwapInt32(&s.state, int32(SessionInited), int32(SessionNormal)) {

		atomic.StoreInt64(&s.zone, zone)
		return true
	}
	return false
}

func (s *Session) CanProcessBusinessRequest() bool {
	state := s.State()
	return state == SessionNormal
}

func (s *Session) CanProcessLoginRequest() bool {
	state := s.State()
	return state == SessionInited || state == SessionNormal
}

func (s *Session) GetOrderedQueue() *OrderedMessageQueue {
	return s.orderedQueue
}

func (s *Session) InitOrderedQueue(maxQueueSize int) {
	if s.orderedQueue == nil {
		s.orderedQueue = NewOrderedMessageQueue(s.ID, maxQueueSize)
	}
}

// ======= 消息保序机制相关方法 =======

// 将notify消息添加到beforeRspNotifies映射中

func (s *Session) AddNotifyBindBeforeRsp(grid uint32, notify *NotifyBindMsgItem) bool {
	if s.orderingManager == nil {
		return false
	}
	return s.orderingManager.AddNotifyBindBeforeRsp(grid, notify)
}

// 将notify消息添加到afterRspNotifies映射中

func (s *Session) AddNotifyBindAfterRsp(grid uint32, notify *NotifyBindMsgItem) bool {
	if s.orderingManager == nil {
		return false
	}
	return s.orderingManager.AddNotifyBindAfterRsp(grid, notify)
}

func (s *Session) IncrementAndGetSeq() uint64 {
	return s.NewServerSeq()
}

func (s *Session) GetOrderingManager() *MessageOrderingManager {
	return s.orderingManager
}

// 清理过期的绑定通知消息
func (s *Session) CleanupExpiredBindNotifies(maxAge int64) int {
	if s.orderingManager == nil {
		return 0
	}
	return s.orderingManager.CleanupExpiredNotifies(time.Now().Unix(), maxAge)
}

// 处理notify消息的入口函数
func (s *Session) ProcessNotify(notifyReq *proto.UnicastPushRequest) error {
	if s == nil || notifyReq == nil {
		return fmt.Errorf("invalid session or notify request")
	}

	notifyData, err := protobuf.Marshal(notifyReq)
	if err != nil {
		return fmt.Errorf("failed to marshal notify request: %w", err)
	}

	notifyItem := &NotifyBindMsgItem{
		NotifyData: notifyData,
		MsgType:    notifyReq.MsgType,
		Title:      notifyReq.Title,
		Content:    notifyReq.Content,
		Metadata:   notifyReq.Metadata,
		SyncHint:   notifyReq.SyncHint,
		BindGrid:   uint32(notifyReq.BindClientSeqId),
		CreateTime: time.Now().Unix(),
	}

	switch notifyReq.SyncHint {
	case proto.NotifySyncHint_NSH_BEFORE_RESPONSE:
		grid := uint32(notifyReq.BindClientSeqId)
		if grid == 0 {
			return fmt.Errorf("bind_client_seq_id is required for NSH_BEFORE_RESPONSE")
		}
		success := s.AddNotifyBindBeforeRsp(grid, notifyItem)
		if !success {
			return fmt.Errorf("failed to bind notify before response for grid: %d", grid)
		}
		return nil

	case proto.NotifySyncHint_NSH_AFTER_RESPONSE:
		grid := uint32(notifyReq.BindClientSeqId)
		if grid == 0 {
			return fmt.Errorf("bind_client_seq_id is required for NSH_AFTER_RESPONSE")
		}
		success := s.AddNotifyBindAfterRsp(grid, notifyItem)
		if !success {
			return fmt.Errorf("failed to bind notify after response for grid: %d", grid)
		}
		return nil

	case proto.NotifySyncHint_NSH_IMMEDIATELY:
		fallthrough
	default:

		return s.sendNotifyImmediately(notifyItem)
	}
}

// 统一处理绑定消息并进行下发的
func (s *Session) ProcWithNotifyBinds(responseData []byte, grid uint32) error {
	if s == nil {
		return fmt.Errorf("invalid session")
	}

	// 1. 下发 Before-Notifies
	if err := s.sendBeforeNotifies(grid); err != nil {
		return fmt.Errorf("failed to send before notifies for grid %d: %w", grid, err)
	}

	// 2. 下发 Response
	if err := s.sendResponse(responseData); err != nil {
		return fmt.Errorf("failed to send response for grid %d: %w", grid, err)
	}

	// 3. 下发 After-Notifies
	if err := s.sendAfterNotifies(grid); err != nil {
		return fmt.Errorf("failed to send after notifies for grid %d: %w", grid, err)
	}

	return nil
}

func (s *Session) sendBeforeNotifies(grid uint32) error {
	if s.orderingManager == nil {
		return nil // 如果没有排序管理器，直接返回
	}

	beforeNotifies := s.orderingManager.GetAndRemoveBeforeNotifies(grid)
	if len(beforeNotifies) == 0 {
		return nil // 没有需要发送的before notify
	}

	for _, notifyItem := range beforeNotifies {

		serverSeq := s.IncrementAndGetSeq()

		if err := s.sendNotifyWithSeq(notifyItem, serverSeq); err != nil {
			return fmt.Errorf("failed to send before notify (seq=%d): %w", serverSeq, err)
		}
	}

	return nil
}

func (s *Session) sendAfterNotifies(grid uint32) error {
	if s.orderingManager == nil {
		return nil // 如果没有排序管理器，直接返回
	}

	afterNotifies := s.orderingManager.GetAndRemoveAfterNotifies(grid)
	if len(afterNotifies) == 0 {
		return nil // 没有需要发送的after notify
	}

	for _, notifyItem := range afterNotifies {
		serverSeq := s.IncrementAndGetSeq()

		if err := s.sendNotifyWithSeq(notifyItem, serverSeq); err != nil {
			return fmt.Errorf("failed to send after notify (seq=%d): %w", serverSeq, err)
		}
	}

	return nil
}

func (s *Session) sendResponse(responseData []byte) error {
	if len(responseData) == 0 {
		return nil
	}

	serverSeq := s.IncrementAndGetSeq()

	serverPush := &proto.ServerPush{
		MsgId:   0,
		SeqId:   serverSeq,
		Type:    proto.PushType_PUSH_BUSINESS_DATA,
		Payload: responseData,
	}

	if s.orderedQueue != nil {
		pushData, err := protobuf.Marshal(serverPush)
		if err != nil {
			return fmt.Errorf("failed to marshal response: %w", err)
		}

		return s.orderedQueue.EnqueueMessage(serverSeq, serverPush, pushData)
	}

	return s.sendMessageDirectly(serverPush)
}

func (s *Session) sendNotifyImmediately(notifyItem *NotifyBindMsgItem) error {
	serverPush := &proto.ServerPush{
		Type:    proto.PushType_PUSH_BUSINESS_DATA,
		SeqId:   s.IncrementAndGetSeq(),
		Payload: notifyItem.NotifyData,
		Headers: notifyItem.Metadata,
	}

	if s.orderedQueue != nil {
		pushData, err := protobuf.Marshal(serverPush)
		if err != nil {
			return fmt.Errorf("failed to marshal notify push: %w", err)
		}

		return s.orderedQueue.EnqueueMessage(serverPush.SeqId, serverPush, pushData)
	}

	return s.sendMessageDirectly(serverPush)
}

// 使用指定序列号发送notify消息
func (s *Session) sendNotifyWithSeq(notifyItem *NotifyBindMsgItem, serverSeq uint64) error {

	serverPush := &proto.ServerPush{
		MsgId:   0,
		SeqId:   serverSeq,
		Type:    proto.PushType_PUSH_BUSINESS_DATA, // notify消息类型
		Payload: notifyItem.NotifyData,
		Headers: notifyItem.Metadata,
	}

	if s.orderedQueue != nil {
		pushData, err := protobuf.Marshal(serverPush)
		if err != nil {
			return fmt.Errorf("failed to marshal server push: %w", err)
		}

		return s.orderedQueue.EnqueueMessage(serverSeq, serverPush, pushData)
	}

	return s.sendMessageDirectly(serverPush)
}

// 直接发送ServerPush消息
func (s *Session) sendMessageDirectly(serverPush *proto.ServerPush) error {
	if s.Stream == nil {
		return fmt.Errorf("session stream is nil")
	}

	pushData, err := protobuf.Marshal(serverPush)
	if err != nil {
		return fmt.Errorf("failed to marshal server push: %w", err)
	}

	s.UpdateActivity()

	_, err = s.Stream.Write(pushData)
	if err != nil {
		return fmt.Errorf("failed to write to stream: %w", err)
	}

	return nil
}
