package backup

import (
	"context"

	"gatesvr/internal/session"
)

// SyncInterface 同步接口
type SyncInterface interface {
	// 启动同步服务
	Start(ctx context.Context) error

	// 停止同步服务
	Stop() error

	// 同步会话数据
	SyncSession(sessionID string, sess *session.Session) error

	// 同步消息队列状态
	SyncQueueState(sessionID string, meta *QueueMetadata) error

	// 批量同步消息
	SyncMessages(sessionID string, messages []*session.OrderedMessage) error

	// 删除会话同步
	DeleteSession(sessionID string) error

	// 全量同步
	FullSync() error

	// 获取同步统计
	GetStats() *SyncStats

	// 检查连接状态
	IsConnected() bool
}

// SyncReceiver 同步接收器接口
type SyncReceiver interface {
	// 启动接收服务
	Start(ctx context.Context) error

	// 停止接收服务
	Stop() error

	// 处理同步消息
	OnSyncReceived(msg *SyncMessage) error

	// 处理心跳消息
	OnHeartbeatReceived(data *HeartbeatData) error

	// 处理全量同步
	OnFullSyncReceived(data *FullSyncData) error

	// 获取接收统计
	GetStats() *SyncStats
}

// FailoverInterface 故障切换接口
type FailoverInterface interface {
	// 启动故障检测
	Start(ctx context.Context) error

	// 停止故障检测
	Stop() error

	// 检测故障
	DetectFailure() bool

	// 执行切换到主模式
	SwitchToPrimary() error

	// 切换到备份模式
	SwitchToBackup() error

	// 恢复会话
	RestoreSessions() error

	// 获取当前模式
	GetMode() ServerMode

	// 检查是否为主服务器
	IsPrimary() bool

	// 检查健康状态
	IsHealthy() bool
}

// HeartbeatInterface 心跳检测接口
type HeartbeatInterface interface {
	// 启动心跳
	Start(ctx context.Context) error

	// 停止心跳
	Stop() error

	// 发送心跳
	SendHeartbeat() error

	// 接收心跳
	OnHeartbeatReceived(data *HeartbeatData) error

	// 检查对端是否存活
	IsPeerAlive() bool

	// 获取最后心跳时间
	GetLastHeartbeatTime() int64
}

// DataValidator 数据校验接口
type DataValidator interface {
	// 校验会话数据
	ValidateSession(data *SessionSyncData) error

	// 校验队列数据
	ValidateQueue(meta *QueueMetadata) error

	// 校验消息数据
	ValidateMessages(messages []*session.OrderedMessage) error

	// 计算校验和
	CalculateChecksum(data interface{}) uint32

	// 验证校验和
	VerifyChecksum(data interface{}, checksum uint32) bool
}

// BackupManager 备份管理器接口
type BackupManager interface {
	// 启动备份服务
	Start(ctx context.Context) error

	// 停止备份服务
	Stop() error

	// 获取当前模式
	GetMode() ServerMode

	// 切换模式
	SwitchMode(mode ServerMode) error

	// 注册会话管理器
	RegisterSessionManager(mgr *session.Manager)

	// 获取统计信息
	GetStats() map[string]interface{}

	// 检查健康状态
	IsHealthy() bool

	// 手动触发同步
	TriggerSync() error

	// 手动触发故障切换
	TriggerFailover() error
}
