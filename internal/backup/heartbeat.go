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

// HeartbeatService 心跳服务（支持发送和监听两种模式）
type HeartbeatService struct {
	config        *SyncConfig
	mode          ServerMode
	serverID      string
	heartbeatAddr string // 心跳地址（分离后的专用地址）

	// 发送模式字段（主服务器使用）
	peerConn    net.Conn
	connMux     sync.RWMutex
	reconnectCh chan struct{}

	// 监听模式字段（备份服务器使用）
	listener    net.Listener
	listenerMux sync.RWMutex
	connections map[string]net.Conn
	connMapMux  sync.RWMutex

	// 共用字段
	sessionMgr     *session.Manager
	lastPeerTime   int64
	lastSelfTime   int64
	timeMux        sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
	stopped        bool
	stopMux        sync.RWMutex
	peerAlive      bool
	aliveMux       sync.RWMutex
	heartbeatStats *HeartbeatStats
	statsMux       sync.RWMutex
}

// HeartbeatStats 心跳统计
type HeartbeatStats struct {
	SentCount        int64         `json:"sent_count"`
	ReceivedCount    int64         `json:"received_count"`
	FailedCount      int64         `json:"failed_count"`
	LastSentTime     time.Time     `json:"last_sent_time"`
	LastRecvTime     time.Time     `json:"last_recv_time"`
	AverageLatency   time.Duration `json:"average_latency"`
	ConsecutiveFails int           `json:"consecutive_fails"`
}

// NewHeartbeatService 创建心跳服务
func NewHeartbeatService(config *SyncConfig, mode ServerMode, serverID string, sessionMgr *session.Manager) HeartbeatInterface {
	return &HeartbeatService{
		config:         config,
		mode:           mode,
		serverID:       serverID,
		heartbeatAddr:  config.PeerAddr, // 默认使用旧的配置，向后兼容
		sessionMgr:     sessionMgr,
		reconnectCh:    make(chan struct{}, 1),
		connections:    make(map[string]net.Conn),
		heartbeatStats: &HeartbeatStats{},
	}
}

// NewHeartbeatServiceWithAddr 创建心跳服务（使用分离的地址）
func NewHeartbeatServiceWithAddr(config *SyncConfig, mode ServerMode, serverID string, sessionMgr *session.Manager, heartbeatAddr string) HeartbeatInterface {
	return &HeartbeatService{
		config:         config,
		mode:           mode,
		serverID:       serverID,
		heartbeatAddr:  heartbeatAddr,
		sessionMgr:     sessionMgr,
		reconnectCh:    make(chan struct{}, 1),
		connections:    make(map[string]net.Conn),
		heartbeatStats: &HeartbeatStats{},
	}
}

// Start 启动心跳服务
func (h *HeartbeatService) Start(ctx context.Context) error {
	h.ctx, h.cancel = context.WithCancel(ctx)

	if h.mode == ModePrimary {
		// 主服务器模式：启动发送功能
		log.Printf("启动心跳发送服务 - 服务器ID: %s, 目标: %s", h.serverID, h.heartbeatAddr)

		// 启动连接管理协程
		go h.connectionManager()

		// 启动心跳发送协程
		go h.heartbeatSender()

	} else {
		// 备份服务器模式：启动监听功能
		log.Printf("启动心跳监听服务 - 服务器ID: %s, 监听: %s", h.serverID, h.heartbeatAddr)

		// 启动监听服务
		if err := h.startListener(); err != nil {
			return fmt.Errorf("启动心跳监听失败: %w", err)
		}

		// 启动连接接收协程
		go h.acceptConnections()
	}

	// 启动心跳检测协程（所有模式都需要）
	go h.heartbeatChecker()

	// 启动统计协程
	go h.statsReporter()

	log.Printf("心跳服务已启动 - 服务器ID: %s, 模式: %s, 间隔: %v",
		h.serverID, h.getModeString(), h.config.HeartbeatInterval)
	return nil
}

// Stop 停止心跳服务
func (h *HeartbeatService) Stop() error {
	h.stopMux.Lock()
	if h.stopped {
		h.stopMux.Unlock()
		return nil
	}
	h.stopped = true
	h.stopMux.Unlock()

	if h.cancel != nil {
		h.cancel()
	}

	// 关闭发送连接
	h.closeConnection()

	// 关闭监听器
	h.closeListener()

	log.Printf("心跳服务已停止")
	return nil
}

// SendHeartbeat 发送心跳
func (h *HeartbeatService) SendHeartbeat() error {
	if !h.IsConnected() {
		return fmt.Errorf("心跳连接未建立")
	}

	// 创建心跳数据
	heartbeatData := &HeartbeatData{
		ServerID:     h.serverID,
		Mode:         h.mode,
		Timestamp:    time.Now().UnixNano(),
		SessionCount: h.getSessionCount(),
		QueueCount:   h.getQueueCount(),
		IsHealthy:    h.isServerHealthy(),
		LastSyncTime: h.getLastSyncTime(),
	}

	// 创建心跳消息
	msg := &SyncMessage{
		Type:      SyncTypeHeartbeat,
		SessionID: "",
		Timestamp: heartbeatData.Timestamp,
		Data:      heartbeatData,
		Checksum:  0, // 心跳消息不需要校验和
	}

	// 发送心跳
	startTime := time.Now()
	err := h.sendHeartbeatMessage(msg)
	latency := time.Since(startTime)

	// 更新统计
	h.updateStats(func(stats *HeartbeatStats) {
		if err != nil {
			stats.FailedCount++
			stats.ConsecutiveFails++
		} else {
			stats.SentCount++
			stats.LastSentTime = time.Now()
			stats.ConsecutiveFails = 0
			// 更新平均延迟
			if stats.AverageLatency == 0 {
				stats.AverageLatency = latency
			} else {
				stats.AverageLatency = (stats.AverageLatency*9 + latency) / 10
			}
		}
	})

	if err != nil {
		log.Printf("发送心跳失败: %v", err)
		// 连接错误，触发重连
		h.closeConnection()
		select {
		case h.reconnectCh <- struct{}{}:
		default:
		}
		return err
	}

	h.updateLastSelfTime(heartbeatData.Timestamp)
	return nil
}

// OnHeartbeatReceived 处理接收到的心跳
func (h *HeartbeatService) OnHeartbeatReceived(data *HeartbeatData) error {
	h.updateLastPeerTime(data.Timestamp)
	h.setPeerAlive(true)

	// 更新统计
	h.updateStats(func(stats *HeartbeatStats) {
		stats.ReceivedCount++
		stats.LastRecvTime = time.Now()
	})

	log.Printf("收到心跳 - 对端: %s, 模式: %s, 会话数: %d, 健康: %v",
		data.ServerID, h.getModeName(data.Mode), data.SessionCount, data.IsHealthy)

	return nil
}

// IsPeerAlive 检查对端是否存活
func (h *HeartbeatService) IsPeerAlive() bool {
	h.aliveMux.RLock()
	defer h.aliveMux.RUnlock()
	return h.peerAlive
}

// GetLastHeartbeatTime 获取最后心跳时间
func (h *HeartbeatService) GetLastHeartbeatTime() int64 {
	h.timeMux.RLock()
	defer h.timeMux.RUnlock()
	return h.lastPeerTime
}

// IsConnected 检查是否已连接
func (h *HeartbeatService) IsConnected() bool {
	h.connMux.RLock()
	defer h.connMux.RUnlock()
	return h.peerConn != nil
}

// GetStats 获取心跳统计
func (h *HeartbeatService) GetStats() *HeartbeatStats {
	h.statsMux.RLock()
	defer h.statsMux.RUnlock()

	return &HeartbeatStats{
		SentCount:        h.heartbeatStats.SentCount,
		ReceivedCount:    h.heartbeatStats.ReceivedCount,
		FailedCount:      h.heartbeatStats.FailedCount,
		LastSentTime:     h.heartbeatStats.LastSentTime,
		LastRecvTime:     h.heartbeatStats.LastRecvTime,
		AverageLatency:   h.heartbeatStats.AverageLatency,
		ConsecutiveFails: h.heartbeatStats.ConsecutiveFails,
	}
}

// connectionManager 连接管理协程
func (h *HeartbeatService) connectionManager() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			if !h.IsConnected() {
				h.tryConnect()
			}
		case <-h.reconnectCh:
			h.tryConnect()
		}
	}
}

// tryConnect 尝试连接
func (h *HeartbeatService) tryConnect() {
	if h.isStopped() {
		return
	}

	h.connMux.Lock()
	defer h.connMux.Unlock()

	if h.peerConn != nil {
		return
	}

	// 使用分离的心跳地址
	conn, err := net.DialTimeout("tcp", h.heartbeatAddr, 10*time.Second)
	if err != nil {
		//log.Printf("心跳连接失败: %v", err)
		return
	}

	h.peerConn = conn
	h.setPeerAlive(true)

	log.Printf("心跳连接建立: %s", h.heartbeatAddr)

	// 启动接收协程
	go h.receiveHeartbeats(conn)
}

// closeConnection 关闭连接
func (h *HeartbeatService) closeConnection() {
	h.connMux.Lock()
	defer h.connMux.Unlock()

	if h.peerConn != nil {
		h.peerConn.Close()
		h.peerConn = nil
		h.setPeerAlive(false)
	}
}

// heartbeatSender 心跳发送协程
func (h *HeartbeatService) heartbeatSender() {
	ticker := time.NewTicker(h.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			if err := h.SendHeartbeat(); err != nil {
				//log.Printf("心跳发送失败: %v", err)
			}
		}
	}
}

// heartbeatChecker 心跳检测协程
func (h *HeartbeatService) heartbeatChecker() {
	ticker := time.NewTicker(h.config.HeartbeatInterval)
	defer ticker.Stop()

	// 故障检测超时（3个心跳周期）
	failureTimeout := h.config.HeartbeatInterval * 3

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			lastTime := h.GetLastHeartbeatTime()
			if lastTime == 0 {
				continue // 还没有收到过心跳
			}

			// 检查是否超时
			timeSinceLastHeartbeat := time.Duration(time.Now().UnixNano() - lastTime)
			if timeSinceLastHeartbeat > failureTimeout {
				if h.IsPeerAlive() {
					log.Printf("检测到对端心跳超时 - 超时时长: %v", timeSinceLastHeartbeat)
					h.setPeerAlive(false)
				}
			}
		}
	}
}

// receiveHeartbeats 接收心跳协程
func (h *HeartbeatService) receiveHeartbeats(conn net.Conn) {
	defer func() {
		h.setPeerAlive(false)
		log.Printf("心跳接收协程退出")
	}()

	for {
		select {
		case <-h.ctx.Done():
			return
		default:
		}

		// 设置读取超时
		conn.SetReadDeadline(time.Now().Add(h.config.HeartbeatInterval * 2))

		// 读取心跳消息
		msg, err := h.readHeartbeatMessage(conn)
		if err != nil {
			if !h.isStopped() {
				log.Printf("读取心跳消息失败: %v", err)
			}
			return
		}

		// 处理心跳消息
		if msg.Type == SyncTypeHeartbeat {
			if heartbeatData, ok := msg.Data.(*HeartbeatData); ok {
				h.OnHeartbeatReceived(heartbeatData)
			}
		}
	}
}

// sendHeartbeatMessage 发送心跳消息
func (h *HeartbeatService) sendHeartbeatMessage(msg *SyncMessage) error {
	h.connMux.RLock()
	conn := h.peerConn
	h.connMux.RUnlock()

	if conn == nil {
		return fmt.Errorf("心跳连接未建立")
	}

	// 序列化消息
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("序列化心跳消息失败: %w", err)
	}

	// 发送消息长度
	length := uint32(len(data))
	if err := writeUint32(conn, length); err != nil {
		return fmt.Errorf("发送心跳消息长度失败: %w", err)
	}

	// 发送消息数据
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("发送心跳消息数据失败: %w", err)
	}

	return nil
}

// readHeartbeatMessage 读取心跳消息
func (h *HeartbeatService) readHeartbeatMessage(conn net.Conn) (*SyncMessage, error) {
	// 读取消息长度
	length, err := readUint32(conn)
	if err != nil {
		return nil, fmt.Errorf("读取心跳消息长度失败: %w", err)
	}

	// 读取消息数据
	data := make([]byte, length)
	if _, err := conn.Read(data); err != nil {
		return nil, fmt.Errorf("读取心跳消息数据失败: %w", err)
	}

	// 反序列化消息
	var msg SyncMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("反序列化心跳消息失败: %w", err)
	}

	// 转换心跳数据
	if msg.Type == SyncTypeHeartbeat {
		dataBytes, err := json.Marshal(msg.Data)
		if err != nil {
			return nil, err
		}

		var heartbeatData HeartbeatData
		if err := json.Unmarshal(dataBytes, &heartbeatData); err != nil {
			return nil, err
		}

		msg.Data = &heartbeatData
	}

	return &msg, nil
}

// statsReporter 统计报告协程
func (h *HeartbeatService) statsReporter() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			stats := h.GetStats()
			log.Printf("心跳统计 - 发送: %d, 接收: %d, 失败: %d, 连续失败: %d, 平均延迟: %v, 对端存活: %v",
				stats.SentCount, stats.ReceivedCount, stats.FailedCount,
				stats.ConsecutiveFails, stats.AverageLatency, h.IsPeerAlive())
		}
	}
}

// 辅助方法

// getSessionCount 获取会话数量
func (h *HeartbeatService) getSessionCount() int {
	if h.sessionMgr != nil {
		return h.sessionMgr.GetSessionCount()
	}
	return 0
}

// getQueueCount 获取队列数量
func (h *HeartbeatService) getQueueCount() int {
	if h.sessionMgr != nil {
		sessions := h.sessionMgr.GetAllSessions()
		count := 0
		for _, sess := range sessions {
			if sess.GetOrderedQueue() != nil {
				count++
			}
		}
		return count
	}
	return 0
}

// isServerHealthy 检查服务器健康状态
func (h *HeartbeatService) isServerHealthy() bool {
	// 简单的健康检查：会话管理器是否正常
	return h.sessionMgr != nil
}

// getLastSyncTime 获取最后同步时间
func (h *HeartbeatService) getLastSyncTime() int64 {
	return time.Now().UnixNano() // 简化实现
}

// updateLastPeerTime 更新对端时间
func (h *HeartbeatService) updateLastPeerTime(timestamp int64) {
	h.timeMux.Lock()
	defer h.timeMux.Unlock()
	h.lastPeerTime = timestamp
}

// updateLastSelfTime 更新自身时间
func (h *HeartbeatService) updateLastSelfTime(timestamp int64) {
	h.timeMux.Lock()
	defer h.timeMux.Unlock()
	h.lastSelfTime = timestamp
}

// setPeerAlive 设置对端存活状态
func (h *HeartbeatService) setPeerAlive(alive bool) {
	h.aliveMux.Lock()
	defer h.aliveMux.Unlock()
	h.peerAlive = alive
}

// updateStats 更新统计
func (h *HeartbeatService) updateStats(fn func(*HeartbeatStats)) {
	h.statsMux.Lock()
	defer h.statsMux.Unlock()
	fn(h.heartbeatStats)
}

// isStopped 检查是否已停止
func (h *HeartbeatService) isStopped() bool {
	h.stopMux.RLock()
	defer h.stopMux.RUnlock()
	return h.stopped
}

// getModeString 获取模式字符串
func (h *HeartbeatService) getModeString() string {
	return h.getModeName(h.mode)
}

// getModeName 获取模式名称
func (h *HeartbeatService) getModeName(mode ServerMode) string {
	switch mode {
	case ModePrimary:
		return "主服务器"
	case ModeBackup:
		return "备份服务器"
	default:
		return "未知"
	}
}

// ============= 监听模式相关方法（备份服务器使用） =============

// startListener 启动监听器
func (h *HeartbeatService) startListener() error {
	listener, err := net.Listen("tcp", h.heartbeatAddr)
	if err != nil {
		return fmt.Errorf("启动心跳监听失败: %w", err)
	}

	h.listenerMux.Lock()
	h.listener = listener
	h.listenerMux.Unlock()

	log.Printf("心跳监听器已启动 - 监听地址: %s", h.heartbeatAddr)
	return nil
}

// closeListener 关闭监听器
func (h *HeartbeatService) closeListener() {
	h.listenerMux.Lock()
	defer h.listenerMux.Unlock()

	if h.listener != nil {
		h.listener.Close()
		h.listener = nil
	}

	// 关闭所有连接
	h.connMapMux.Lock()
	for addr, conn := range h.connections {
		conn.Close()
		delete(h.connections, addr)
	}
	h.connMapMux.Unlock()
}

// acceptConnections 接受连接协程
func (h *HeartbeatService) acceptConnections() {
	defer func() {
		log.Printf("心跳连接接受协程退出")
	}()

	for {
		select {
		case <-h.ctx.Done():
			return
		default:
		}

		h.listenerMux.RLock()
		listener := h.listener
		h.listenerMux.RUnlock()

		if listener == nil {
			return
		}

		// 设置接受超时
		if tcpListener, ok := listener.(*net.TCPListener); ok {
			tcpListener.SetDeadline(time.Now().Add(time.Second))
		}

		conn, err := listener.Accept()
		if err != nil {
			if !h.isStopped() {
				// 只在非超时错误时记录日志
				if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
					log.Printf("接受心跳连接失败: %v", err)
				}
			}
			continue
		}

		// 处理新连接
		clientAddr := conn.RemoteAddr().String()
		log.Printf("新的心跳连接: %s", clientAddr)

		// 保存连接
		h.connMapMux.Lock()
		h.connections[clientAddr] = conn
		h.connMapMux.Unlock()

		// 启动处理协程
		go h.handleHeartbeatConnection(conn, clientAddr)
	}
}

// handleHeartbeatConnection 处理心跳连接
func (h *HeartbeatService) handleHeartbeatConnection(conn net.Conn, clientAddr string) {
	defer func() {
		conn.Close()
		h.connMapMux.Lock()
		delete(h.connections, clientAddr)
		h.connMapMux.Unlock()
		log.Printf("心跳连接关闭: %s", clientAddr)
	}()

	for {
		select {
		case <-h.ctx.Done():
			return
		default:
		}

		// 设置读取超时
		conn.SetReadDeadline(time.Now().Add(h.config.HeartbeatInterval * 3))

		// 读取心跳消息
		msg, err := h.readHeartbeatMessage(conn)
		if err != nil {
			if !h.isStopped() {
				log.Printf("读取心跳消息失败 [%s]: %v", clientAddr, err)
			}
			return
		}

		// 处理心跳消息
		if msg.Type == SyncTypeHeartbeat {
			if heartbeatData, ok := msg.Data.(*HeartbeatData); ok {
				h.OnHeartbeatReceived(heartbeatData)

				// 发送心跳响应
				h.sendHeartbeatResponse(conn, heartbeatData)
			}
		}
	}
}

// sendHeartbeatResponse 发送心跳响应
func (h *HeartbeatService) sendHeartbeatResponse(conn net.Conn, receivedData *HeartbeatData) error {
	// 创建响应数据
	responseData := &HeartbeatData{
		ServerID:     h.serverID,
		Mode:         h.mode,
		Timestamp:    time.Now().UnixNano(),
		SessionCount: h.getSessionCount(),
		QueueCount:   h.getQueueCount(),
		IsHealthy:    h.isServerHealthy(),
		LastSyncTime: h.getLastSyncTime(),
	}

	// 创建响应消息
	msg := &SyncMessage{
		Type:      SyncTypeHeartbeat,
		SessionID: "",
		Timestamp: responseData.Timestamp,
		Data:      responseData,
		Checksum:  0,
	}

	// 发送响应
	return h.sendHeartbeatToConnection(conn, msg)
}

// sendHeartbeatToConnection 发送心跳消息到指定连接
func (h *HeartbeatService) sendHeartbeatToConnection(conn net.Conn, msg *SyncMessage) error {
	// 序列化消息
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("序列化心跳消息失败: %w", err)
	}

	// 发送消息长度
	length := uint32(len(data))
	if err := writeUint32(conn, length); err != nil {
		return fmt.Errorf("发送心跳消息长度失败: %w", err)
	}

	// 发送消息数据
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("发送心跳消息数据失败: %w", err)
	}

	return nil
}
