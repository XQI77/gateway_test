package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"gatesvr/internal/session"
)

// SyncService 数据同步服务
type SyncService struct {
	config      *SyncConfig
	syncAddr    string // 同步地址（分离后的专用地址）
	peerConn    net.Conn
	connMux     sync.RWMutex
	syncChan    chan *SyncMessage
	stats       *SyncStats
	statsMux    sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	stopped     bool
	stopMux     sync.RWMutex
	reconnectCh chan struct{}
	validator   DataValidator
}

// NewSyncService 创建同步服务
func NewSyncService(config *SyncConfig) *SyncService {
	return &SyncService{
		config:      config,
		syncAddr:    config.PeerAddr, // 默认使用旧的配置，向后兼容
		syncChan:    make(chan *SyncMessage, config.BufferSize),
		reconnectCh: make(chan struct{}, 1),
		stats: &SyncStats{
			LastSyncTime: time.Now(),
		},
		validator: NewDataValidator(),
	}
}

// NewSyncServiceWithAddr 创建同步服务（使用分离的地址）
func NewSyncServiceWithAddr(config *SyncConfig, syncAddr string) *SyncService {
	return &SyncService{
		config:      config,
		syncAddr:    syncAddr,
		syncChan:    make(chan *SyncMessage, config.BufferSize),
		reconnectCh: make(chan struct{}, 1),
		stats: &SyncStats{
			LastSyncTime: time.Now(),
		},
		validator: NewDataValidator(),
	}
}

// Start 启动同步服务
func (s *SyncService) Start(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	// 启动连接管理协程
	go s.connectionManager()

	// 启动同步处理协程
	go s.syncProcessor()

	// 启动统计更新协程
	go s.statsUpdater()

	log.Printf("同步服务已启动 - 模式: %s, 对端: %s", s.getModeString(), s.syncAddr)
	return nil
}

// Stop 停止同步服务
func (s *SyncService) Stop() error {
	s.stopMux.Lock()
	if s.stopped {
		s.stopMux.Unlock()
		return nil
	}
	s.stopped = true
	s.stopMux.Unlock()

	if s.cancel != nil {
		s.cancel()
	}

	s.closeConnection()
	close(s.syncChan)

	log.Printf("同步服务已停止")
	return nil
}

// SyncSession 同步会话数据
func (s *SyncService) SyncSession(sessionID string, sess *session.Session) error {
	if !s.IsConnected() {
		return fmt.Errorf("同步服务未连接")
	}

	// 转换会话数据
	syncData := s.convertSessionToSyncData(sess)

	// 创建同步消息
	msg := &SyncMessage{
		Type:      SyncTypeUpdateSession,
		SessionID: sessionID,
		Timestamp: time.Now().UnixNano(),
		Data:      syncData,
	}

	// 计算校验和
	msg.Checksum = s.validator.CalculateChecksum(syncData)

	// 发送到同步队列
	select {
	case s.syncChan <- msg:
		return nil
	default:
		return fmt.Errorf("同步队列已满")
	}
}

// SyncQueueState 同步队列状态
func (s *SyncService) SyncQueueState(sessionID string, meta *QueueMetadata) error {
	if !s.IsConnected() {
		return fmt.Errorf("同步服务未连接")
	}

	msg := &SyncMessage{
		Type:      SyncTypeSyncQueue,
		SessionID: sessionID,
		Timestamp: time.Now().UnixNano(),
		Data:      meta,
		Checksum:  s.validator.CalculateChecksum(meta),
	}

	select {
	case s.syncChan <- msg:
		return nil
	default:
		return fmt.Errorf("同步队列已满")
	}
}

// SyncMessages 批量同步消息
func (s *SyncService) SyncMessages(sessionID string, messages []*session.OrderedMessage) error {
	if !s.IsConnected() || len(messages) == 0 {
		return fmt.Errorf("同步服务未连接或消息为空")
	}

	msg := &SyncMessage{
		Type:      SyncTypeSyncMessages,
		SessionID: sessionID,
		Timestamp: time.Now().UnixNano(),
		Data:      messages,
		Checksum:  s.validator.CalculateChecksum(messages),
	}

	select {
	case s.syncChan <- msg:
		return nil
	default:
		return fmt.Errorf("同步队列已满")
	}
}

// DeleteSession 删除会话同步
func (s *SyncService) DeleteSession(sessionID string) error {
	if !s.IsConnected() {
		return fmt.Errorf("同步服务未连接")
	}

	msg := &SyncMessage{
		Type:      SyncTypeDeleteSession,
		SessionID: sessionID,
		Timestamp: time.Now().UnixNano(),
		Data:      nil,
		Checksum:  0,
	}

	select {
	case s.syncChan <- msg:
		return nil
	default:
		return fmt.Errorf("同步队列已满")
	}
}

// FullSync 全量同步
func (s *SyncService) FullSync() error {
	if !s.IsConnected() {
		return fmt.Errorf("同步服务未连接")
	}

	// 创建全量同步消息
	msg := &SyncMessage{
		Type:      SyncTypeFullSync,
		SessionID: "",
		Timestamp: time.Now().UnixNano(),
		Data:      nil,
		Checksum:  0,
	}

	select {
	case s.syncChan <- msg:
		log.Printf("全量同步请求已发送")
		return nil
	default:
		return fmt.Errorf("同步队列已满")
	}
}

// GetStats 获取同步统计
func (s *SyncService) GetStats() *SyncStats {
	s.statsMux.RLock()
	defer s.statsMux.RUnlock()

	// 返回副本
	return &SyncStats{
		SentCount:     s.stats.SentCount,
		ReceivedCount: s.stats.ReceivedCount,
		ErrorCount:    s.stats.ErrorCount,
		LastSyncTime:  s.stats.LastSyncTime,
		AvgLatency:    s.stats.AvgLatency,
		IsConnected:   s.stats.IsConnected,
	}
}

// IsConnected 检查连接状态
func (s *SyncService) IsConnected() bool {
	s.connMux.RLock()
	defer s.connMux.RUnlock()
	return s.peerConn != nil
}

// connectionManager 连接管理协程
func (s *SyncService) connectionManager() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if !s.IsConnected() {
				s.tryConnect()
			}
		case <-s.reconnectCh:
			s.tryConnect()
		}
	}
}

// tryConnect 尝试连接对端
func (s *SyncService) tryConnect() {
	s.connMux.Lock()
	defer s.connMux.Unlock()

	if s.peerConn != nil {
		return
	}

	conn, err := net.DialTimeout("tcp", s.syncAddr, 10*time.Second)
	if err != nil {
		//log.Printf("连接对端失败: %v", err)
		s.updateStats(func(stats *SyncStats) {
			stats.ErrorCount++
			stats.IsConnected = false
		})
		return
	}

	s.peerConn = conn
	s.updateStats(func(stats *SyncStats) {
		stats.IsConnected = true
	})

	log.Printf("连接对端成功: %s", s.syncAddr)
}

// closeConnection 关闭连接
func (s *SyncService) closeConnection() {
	s.connMux.Lock()
	defer s.connMux.Unlock()

	if s.peerConn != nil {
		s.peerConn.Close()
		s.peerConn = nil
		s.updateStats(func(stats *SyncStats) {
			stats.IsConnected = false
		})
	}
}

// syncProcessor 同步处理协程
func (s *SyncService) syncProcessor() {
	batch := make([]*SyncMessage, 0, s.config.SyncBatchSize)
	ticker := time.NewTicker(s.config.SyncTimeout)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			// 处理剩余批次
			if len(batch) > 0 {
				s.sendBatch(batch)
			}
			return

		case msg, ok := <-s.syncChan:
			if !ok {
				return
			}

			batch = append(batch, msg)

			// 批次满了或者是紧急消息，立即发送
			if len(batch) >= s.config.SyncBatchSize || s.isUrgentMessage(msg) {
				s.sendBatch(batch)
				batch = batch[:0] // 清空但保留容量
				ticker.Reset(s.config.SyncTimeout)
			}

		case <-ticker.C:
			// 超时，发送当前批次
			if len(batch) > 0 {
				s.sendBatch(batch)
				batch = batch[:0]
			}
			ticker.Reset(s.config.SyncTimeout)
		}
	}
}

// sendBatch 发送批次消息
func (s *SyncService) sendBatch(batch []*SyncMessage) {
	if !s.IsConnected() {
		log.Printf("连接断开，跳过批次发送，消息数: %d", len(batch))
		return
	}

	startTime := time.Now()

	for _, msg := range batch {
		if err := s.sendMessage(msg); err != nil {
			log.Printf("发送同步消息失败: %v", err)
			s.updateStats(func(stats *SyncStats) {
				stats.ErrorCount++
			})

			// 连接错误，触发重连
			if isConnectionError(err) {
				s.closeConnection()
				select {
				case s.reconnectCh <- struct{}{}:
				default:
				}
				break
			}
		} else {
			s.updateStats(func(stats *SyncStats) {
				stats.SentCount++
			})
		}
	}

	// 更新延迟统计
	latency := time.Since(startTime)
	s.updateStats(func(stats *SyncStats) {
		stats.LastSyncTime = time.Now()
		// 简单的移动平均
		if stats.AvgLatency == 0 {
			stats.AvgLatency = latency
		} else {
			stats.AvgLatency = (stats.AvgLatency*9 + latency) / 10
		}
	})

	log.Printf("批次同步完成 - 消息数: %d, 延迟: %v", len(batch), latency)
}

// sendMessage 发送单个消息
func (s *SyncService) sendMessage(msg *SyncMessage) error {
	s.connMux.RLock()
	conn := s.peerConn
	s.connMux.RUnlock()

	if conn == nil {
		return fmt.Errorf("连接未建立")
	}

	// 序列化消息
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("序列化消息失败: %w", err)
	}

	// 发送消息长度
	length := uint32(len(data))
	if err := writeUint32(conn, length); err != nil {
		return fmt.Errorf("发送消息长度失败: %w", err)
	}

	// 发送消息数据
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("发送消息数据失败: %w", err)
	}

	return nil
}

// statsUpdater 统计更新协程
func (s *SyncService) statsUpdater() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			// 定期输出统计信息
			stats := s.GetStats()
			log.Printf("同步统计 - 发送: %d, 接收: %d, 错误: %d, 连接: %v, 平均延迟: %v",
				stats.SentCount, stats.ReceivedCount, stats.ErrorCount,
				stats.IsConnected, stats.AvgLatency)
		}
	}
}

// convertSessionToSyncData 转换会话数据为同步数据
func (s *SyncService) convertSessionToSyncData(sess *session.Session) *SessionSyncData {
	// 获取队列元数据
	var queueMeta *QueueMetadata
	var pendingMsgs []*session.OrderedMessage

	if orderedQueue := sess.GetOrderedQueue(); orderedQueue != nil {
		stats := orderedQueue.GetQueueStats()
		queueMeta = &QueueMetadata{
			NextExpectedSeq: stats["next_expected_seq"].(uint64),
			LastSentSeq:     stats["last_sent_seq"].(uint64),
			LastAckedSeq:    stats["last_acked_seq"].(uint64),
			PendingCount:    stats["pending_count"].(int),
		}

		// 注意: GetPendingMessages方法已被移除，现在无法获取具体的待确认消息
		// 如果需要此功能，需要重新实现或调整同步策略
		pendingMsgs = []*session.OrderedMessage{} // 临时设为空切片
	}

	return &SessionSyncData{
		SessionID:    sess.ID,
		OpenID:       sess.OpenID,
		ClientID:     sess.ClientID,
		UserIP:       sess.UserIP,
		State:        int32(sess.State()),
		Zone:         sess.Zone(),
		ServerSeq:    sess.ServerSeq(),
		MaxClientSeq: sess.MaxClientSeq(),
		CreateTime:   sess.CreateTime,
		LastActivity: sess.LastActivity,
		QueueMeta:    queueMeta,
		PendingMsgs:  pendingMsgs,
	}
}

// isUrgentMessage 判断是否为紧急消息
func (s *SyncService) isUrgentMessage(msg *SyncMessage) bool {
	switch msg.Type {
	case SyncTypeDeleteSession, SyncTypeFullSync:
		return true
	default:
		return false
	}
}

// updateStats 线程安全地更新统计
func (s *SyncService) updateStats(fn func(*SyncStats)) {
	s.statsMux.Lock()
	defer s.statsMux.Unlock()
	fn(s.stats)
}

// getModeString 获取模式字符串
func (s *SyncService) getModeString() string {
	if s.config.Mode == ModePrimary {
		return "主服务器"
	}
	return "备份服务器"
}

// isConnectionError 判断是否为连接错误
func isConnectionError(err error) bool {
	if netErr, ok := err.(net.Error); ok {
		return netErr.Timeout() || netErr.Temporary()
	}
	return true // 保守处理，其他错误也认为是连接问题
}

// writeUint32 写入32位整数
func writeUint32(conn net.Conn, value uint32) error {
	buf := make([]byte, 4)
	buf[0] = byte(value >> 24)
	buf[1] = byte(value >> 16)
	buf[2] = byte(value >> 8)
	buf[3] = byte(value)
	_, err := conn.Write(buf)
	return err
}
