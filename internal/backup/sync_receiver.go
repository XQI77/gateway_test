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

// syncReceiverImpl 同步接收器实现
type syncReceiverImpl struct {
	config      *SyncConfig
	listenAddr  string // 监听地址（分离后的专用地址）
	listener    net.Listener
	sessionMgr  *session.Manager
	stats       *SyncStats
	statsMux    sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	stopped     bool
	stopMux     sync.RWMutex
	validator   DataValidator
	connections map[string]net.Conn
	connMux     sync.RWMutex
}

// newSyncReceiver 创建同步接收器
func newSyncReceiver(config *SyncConfig, sessionMgr *session.Manager) SyncReceiver {
	return &syncReceiverImpl{
		config:      config,
		listenAddr:  config.PeerAddr, // 默认使用旧的配置，向后兼容
		sessionMgr:  sessionMgr,
		stats:       &SyncStats{},
		validator:   NewDataValidator(),
		connections: make(map[string]net.Conn),
	}
}

// newSyncReceiverWithAddr 创建同步接收器（使用分离的地址）
func newSyncReceiverWithAddr(config *SyncConfig, sessionMgr *session.Manager, listenAddr string) SyncReceiver {
	return &syncReceiverImpl{
		config:      config,
		listenAddr:  listenAddr,
		sessionMgr:  sessionMgr,
		stats:       &SyncStats{},
		validator:   NewDataValidator(),
		connections: make(map[string]net.Conn),
	}
}

// Start 启动接收服务
func (r *syncReceiverImpl) Start(ctx context.Context) error {
	r.ctx, r.cancel = context.WithCancel(ctx)

	// 启动TCP监听
	listener, err := net.Listen("tcp", r.listenAddr)
	if err != nil {
		return fmt.Errorf("启动监听失败: %w", err)
	}

	r.listener = listener

	// 启动接受连接的协程
	go r.acceptConnections()

	// 启动统计协程
	go r.statsUpdater()

	log.Printf("同步接收器已启动 - 监听地址: %s", r.listenAddr)
	return nil
}

// Stop 停止接收服务
func (r *syncReceiverImpl) Stop() error {
	r.stopMux.Lock()
	if r.stopped {
		r.stopMux.Unlock()
		return nil
	}
	r.stopped = true
	r.stopMux.Unlock()

	if r.cancel != nil {
		r.cancel()
	}

	if r.listener != nil {
		r.listener.Close()
	}

	// 关闭所有连接
	r.connMux.Lock()
	for _, conn := range r.connections {
		conn.Close()
	}
	r.connections = make(map[string]net.Conn)
	r.connMux.Unlock()

	log.Printf("同步接收器已停止")
	return nil
}

// acceptConnections 接受连接
func (r *syncReceiverImpl) acceptConnections() {
	for {
		select {
		case <-r.ctx.Done():
			return
		default:
		}

		conn, err := r.listener.Accept()
		if err != nil {
			if r.isStopped() {
				return
			}
			log.Printf("接受连接失败: %v", err)
			continue
		}

		// 为每个连接启动处理协程
		go r.handleConnection(conn)
	}
}

// handleConnection 处理连接
func (r *syncReceiverImpl) handleConnection(conn net.Conn) {
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	log.Printf("新的同步连接: %s", remoteAddr)

	// 注册连接
	r.connMux.Lock()
	r.connections[remoteAddr] = conn
	r.connMux.Unlock()

	// 连接结束时清理
	defer func() {
		r.connMux.Lock()
		delete(r.connections, remoteAddr)
		r.connMux.Unlock()
		log.Printf("同步连接断开: %s", remoteAddr)
	}()

	// 处理消息
	for {
		select {
		case <-r.ctx.Done():
			return
		default:
		}

		// 读取消息
		msg, err := r.readMessage(conn)
		if err != nil {
			if !r.isStopped() {
				log.Printf("读取同步消息失败: %v", err)
			}
			return
		}

		// 处理消息
		if err := r.processMessage(msg); err != nil {
			log.Printf("处理同步消息失败: %v", err)
			r.updateStats(func(stats *SyncStats) {
				stats.ErrorCount++
			})
		} else {
			r.updateStats(func(stats *SyncStats) {
				stats.ReceivedCount++
				stats.LastSyncTime = time.Now()
			})
		}
	}
}

// readMessage 读取消息
func (r *syncReceiverImpl) readMessage(conn net.Conn) (*SyncMessage, error) {
	// 读取消息长度
	length, err := readUint32(conn)
	if err != nil {
		return nil, fmt.Errorf("读取消息长度失败: %w", err)
	}

	// 防止过大的消息
	if length > 10*1024*1024 { // 10MB限制
		return nil, fmt.Errorf("消息过大: %d bytes", length)
	}

	// 读取消息数据
	data := make([]byte, length)
	if _, err := conn.Read(data); err != nil {
		return nil, fmt.Errorf("读取消息数据失败: %w", err)
	}

	// 反序列化消息
	var msg SyncMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("反序列化消息失败: %w", err)
	}

	return &msg, nil
}

// processMessage 处理消息
func (r *syncReceiverImpl) processMessage(msg *SyncMessage) error {
	switch msg.Type {
	case SyncTypeCreateSession, SyncTypeUpdateSession:
		return r.handleSessionSync(msg)
	case SyncTypeDeleteSession:
		return r.handleSessionDelete(msg)
	case SyncTypeSyncQueue:
		return r.handleQueueSync(msg)
	case SyncTypeSyncMessages:
		return r.handleMessageSync(msg)
	case SyncTypeHeartbeat:
		return r.handleHeartbeat(msg)
	case SyncTypeFullSync:
		return r.handleFullSyncRequest(msg)
	default:
		return fmt.Errorf("未知消息类型: %d", msg.Type)
	}
}

// handleSessionSync 处理会话同步
func (r *syncReceiverImpl) handleSessionSync(msg *SyncMessage) error {
	// 解析会话数据
	syncDataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		return fmt.Errorf("序列化会话数据失败: %w", err)
	}

	var syncData SessionSyncData
	if err := json.Unmarshal(syncDataBytes, &syncData); err != nil {
		return fmt.Errorf("反序列化会话数据失败: %w", err)
	}

	// 校验数据
	if err := r.validator.ValidateSession(&syncData); err != nil {
		return fmt.Errorf("会话数据校验失败: %w", err)
	}

	// 校验校验和
	if !r.validator.VerifyChecksum(&syncData, msg.Checksum) {
		return fmt.Errorf("校验和验证失败")
	}

	// 在备份模式下，更新本地会话数据
	return r.updateLocalSession(&syncData)
}

// handleSessionDelete 处理会话删除
func (r *syncReceiverImpl) handleSessionDelete(msg *SyncMessage) error {
	// 在备份模式下，删除本地会话
	r.sessionMgr.RemoveSession(msg.SessionID)
	log.Printf("同步删除会话: %s", msg.SessionID)
	return nil
}

// handleQueueSync 处理队列同步
func (r *syncReceiverImpl) handleQueueSync(msg *SyncMessage) error {
	// 解析队列元数据
	metaBytes, err := json.Marshal(msg.Data)
	if err != nil {
		return fmt.Errorf("序列化队列元数据失败: %w", err)
	}

	var meta QueueMetadata
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return fmt.Errorf("反序列化队列元数据失败: %w", err)
	}

	// 校验数据
	if err := r.validator.ValidateQueue(&meta); err != nil {
		return fmt.Errorf("队列数据校验失败: %w", err)
	}

	// 更新本地队列状态
	return r.updateLocalQueue(msg.SessionID, &meta)
}

// handleMessageSync 处理消息同步
func (r *syncReceiverImpl) handleMessageSync(msg *SyncMessage) error {
	// 解析消息列表
	msgsBytes, err := json.Marshal(msg.Data)
	if err != nil {
		return fmt.Errorf("序列化消息列表失败: %w", err)
	}

	var messages []*session.OrderedMessage
	if err := json.Unmarshal(msgsBytes, &messages); err != nil {
		return fmt.Errorf("反序列化消息列表失败: %w", err)
	}

	// 校验消息
	if err := r.validator.ValidateMessages(messages); err != nil {
		return fmt.Errorf("消息数据校验失败: %w", err)
	}

	// 更新本地消息
	return r.updateLocalMessages(msg.SessionID, messages)
}

// handleHeartbeat 处理心跳
func (r *syncReceiverImpl) handleHeartbeat(msg *SyncMessage) error {
	// 解析心跳数据
	hbBytes, err := json.Marshal(msg.Data)
	if err != nil {
		return fmt.Errorf("序列化心跳数据失败: %w", err)
	}

	var heartbeat HeartbeatData
	if err := json.Unmarshal(hbBytes, &heartbeat); err != nil {
		return fmt.Errorf("反序列化心跳数据失败: %w", err)
	}

	return r.OnHeartbeatReceived(&heartbeat)
}

// handleFullSyncRequest 处理全量同步请求
func (r *syncReceiverImpl) handleFullSyncRequest(msg *SyncMessage) error {
	log.Printf("收到全量同步请求")
	// 这里可以实现发送全量数据的逻辑
	// 在实际实现中，备份服务器通常不需要响应全量同步请求
	return nil
}

// OnSyncReceived 处理同步消息（接口实现）
func (r *syncReceiverImpl) OnSyncReceived(msg *SyncMessage) error {
	return r.processMessage(msg)
}

// OnHeartbeatReceived 处理心跳消息（接口实现）
func (r *syncReceiverImpl) OnHeartbeatReceived(data *HeartbeatData) error {
	log.Printf("收到心跳 - 服务器: %s, 模式: %d, 会话数: %d",
		data.ServerID, data.Mode, data.SessionCount)
	return nil
}

// OnFullSyncReceived 处理全量同步（接口实现）
func (r *syncReceiverImpl) OnFullSyncReceived(data *FullSyncData) error {
	log.Printf("收到全量同步数据 - 会话数: %d, 批次: %d/%d",
		len(data.Sessions), data.BatchIndex+1, data.TotalBatches)

	// 处理每个会话
	for _, sessionData := range data.Sessions {
		if err := r.updateLocalSession(sessionData); err != nil {
			log.Printf("更新会话失败: %v", err)
		}
	}

	return nil
}

// GetStats 获取接收统计
func (r *syncReceiverImpl) GetStats() *SyncStats {
	r.statsMux.RLock()
	defer r.statsMux.RUnlock()

	return &SyncStats{
		SentCount:     r.stats.SentCount,
		ReceivedCount: r.stats.ReceivedCount,
		ErrorCount:    r.stats.ErrorCount,
		LastSyncTime:  r.stats.LastSyncTime,
		AvgLatency:    r.stats.AvgLatency,
		IsConnected:   len(r.connections) > 0,
	}
}

// updateLocalSession 更新本地会话
func (r *syncReceiverImpl) updateLocalSession(syncData *SessionSyncData) error {
	if r.config.ReadOnly {
		// 只读模式下，不实际修改数据，仅记录日志
		log.Printf("只读模式 - 模拟更新会话: %s", syncData.SessionID)
		return nil
	}

	// 在实际实现中，这里需要根据同步数据创建或更新会话
	// 由于当前的session.Manager没有直接的更新接口，这里暂时只记录日志 TODO
	log.Printf("同步更新会话: %s, OpenID: %s, GID: %d, 状态: %d",
		syncData.SessionID, syncData.OpenID, syncData.Gid, syncData.State)

	return nil
}

// updateLocalQueue 更新本地队列状态
func (r *syncReceiverImpl) updateLocalQueue(sessionID string, meta *QueueMetadata) error {
	if r.config.ReadOnly {
		log.Printf("只读模式 - 模拟更新队列: %s", sessionID)
		return nil
	}

	log.Printf("同步更新队列 - 会话: %s, 期望序列号: %d, 待确认: %d",
		sessionID, meta.NextExpectedSeq, meta.PendingCount)

	return nil
}

// updateLocalMessages 更新本地消息
func (r *syncReceiverImpl) updateLocalMessages(sessionID string, messages []*session.OrderedMessage) error {
	if r.config.ReadOnly {
		log.Printf("只读模式 - 模拟更新消息: %s, 数量: %d", sessionID, len(messages))
		return nil
	}

	log.Printf("同步更新消息 - 会话: %s, 消息数量: %d", sessionID, len(messages))
	return nil
}

// statsUpdater 统计更新协程
func (r *syncReceiverImpl) statsUpdater() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			stats := r.GetStats()
			log.Printf("接收统计 - 接收: %d, 错误: %d, 连接数: %d",
				stats.ReceivedCount, stats.ErrorCount, len(r.connections))
		}
	}
}

// updateStats 线程安全地更新统计
func (r *syncReceiverImpl) updateStats(fn func(*SyncStats)) {
	r.statsMux.Lock()
	defer r.statsMux.Unlock()
	fn(r.stats)
}

// isStopped 检查是否已停止
func (r *syncReceiverImpl) isStopped() bool {
	r.stopMux.RLock()
	defer r.stopMux.RUnlock()
	return r.stopped
}

// readUint32 读取32位整数
func readUint32(conn net.Conn) (uint32, error) {
	buf := make([]byte, 4)
	if _, err := conn.Read(buf); err != nil {
		return 0, err
	}
	return uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3]), nil
}
