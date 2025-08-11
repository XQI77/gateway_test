// Package backup 提供热备份功能
package backup

import (
	"time"

	"gatesvr/internal/session"
)

// SyncType 同步消息类型
type SyncType int32

const (
	SyncTypeCreateSession SyncType = iota + 1 // 创建会话
	SyncTypeUpdateSession                     // 更新会话
	SyncTypeDeleteSession                     // 删除会话
	SyncTypeSyncQueue                         // 同步队列状态
	SyncTypeSyncMessages                      // 同步消息内容
	SyncTypeHeartbeat                         // 心跳消息
	SyncTypeFullSync                          // 全量同步
)

// ServerMode 服务器模式
type ServerMode int32

const (
	ModePrimary ServerMode = iota + 1 // 主服务器
	ModeBackup                        // 备份服务器
)

// SyncMessage 同步消息结构
type SyncMessage struct {
	Type      SyncType    `json:"type"`       // 消息类型
	SessionID string      `json:"session_id"` // 会话ID
	Timestamp int64       `json:"timestamp"`  // 时间戳
	Data      interface{} `json:"data"`       // 具体数据
	Checksum  uint32      `json:"checksum"`   // 数据校验和
}

// SessionSyncData 会话同步数据
type SessionSyncData struct {
	SessionID    string                    `json:"session_id"`
	OpenID       string                    `json:"open_id"`
	ClientID     string                    `json:"client_id"`
	AccessToken  string                    `json:"access_token"`
	UserIP       string                    `json:"user_ip"`
	State        int32                     `json:"state"`
	Gid          int64                     `json:"gid"`
	Zone         int64                     `json:"zone"`
	ServerSeq    uint64                    `json:"server_seq"`
	MaxClientSeq uint64                    `json:"max_client_seq"`
	CreateTime   time.Time                 `json:"create_time"`
	LastActivity time.Time                 `json:"last_activity"`
	QueueMeta    *QueueMetadata            `json:"queue_meta"`
	PendingMsgs  []*session.OrderedMessage `json:"pending_msgs,omitempty"`
}

// QueueMetadata 队列元数据
type QueueMetadata struct {
	NextExpectedSeq uint64 `json:"next_expected_seq"` // 下一个期望序列号
	LastSentSeq     uint64 `json:"last_sent_seq"`     // 最后发送序列号
	LastAckedSeq    uint64 `json:"last_acked_seq"`    // 最后确认序列号
	PendingCount    int    `json:"pending_count"`     // 待确认数量
	QueueSize       int    `json:"queue_size"`        // 等待队列长度
}

// HeartbeatData 心跳数据
type HeartbeatData struct {
	ServerID      string    `json:"server_id"`      // 服务器ID
	Mode          ServerMode `json:"mode"`          // 服务器模式
	Timestamp     int64     `json:"timestamp"`      // 时间戳
	SessionCount  int       `json:"session_count"`  // 会话数量
	QueueCount    int       `json:"queue_count"`    // 队列总数
	IsHealthy     bool      `json:"is_healthy"`     // 健康状态
	LastSyncTime  int64     `json:"last_sync_time"` // 最后同步时间
}

// FullSyncData 全量同步数据
type FullSyncData struct {
	ServerID     string             `json:"server_id"`
	Timestamp    int64              `json:"timestamp"`
	Sessions     []*SessionSyncData `json:"sessions"`
	TotalCount   int                `json:"total_count"`
	BatchIndex   int                `json:"batch_index"`
	TotalBatches int                `json:"total_batches"`
}

// SyncConfig 同步配置
type SyncConfig struct {
	Enabled           bool          `json:"enabled"`             // 是否启用备份
	Mode              ServerMode    `json:"mode"`               // 服务器模式
	PeerAddr          string        `json:"peer_addr"`          // 对端地址
	HeartbeatInterval time.Duration `json:"heartbeat_interval"` // 心跳间隔
	SyncBatchSize     int           `json:"sync_batch_size"`    // 同步批次大小
	SyncTimeout       time.Duration `json:"sync_timeout"`       // 同步超时
	ReadOnly          bool          `json:"readonly"`           // 只读模式
	BufferSize        int           `json:"buffer_size"`        // 缓冲区大小
}

// FailoverConfig 故障切换配置
type FailoverConfig struct {
	DetectionTimeout time.Duration `json:"detection_timeout"` // 故障检测超时
	SwitchTimeout    time.Duration `json:"switch_timeout"`    // 切换超时
	RecoveryTimeout  time.Duration `json:"recovery_timeout"`  // 恢复超时
	MaxRetries       int           `json:"max_retries"`       // 最大重试次数
}

// BackupConfig 备份总配置
type BackupConfig struct {
	Sync          SyncConfig     `json:"sync"`          // 同步配置
	Failover      FailoverConfig `json:"failover"`      // 故障切换配置
	HeartbeatAddr string         `json:"heartbeat_addr"` // 心跳地址（分离后的）
	SyncAddr      string         `json:"sync_addr"`      // 同步地址（分离后的）
}

// SyncStats 同步统计
type SyncStats struct {
	SentCount     int64         `json:"sent_count"`     // 发送计数
	ReceivedCount int64         `json:"received_count"` // 接收计数
	ErrorCount    int64         `json:"error_count"`    // 错误计数
	LastSyncTime  time.Time     `json:"last_sync_time"` // 最后同步时间
	AvgLatency    time.Duration `json:"avg_latency"`    // 平均延迟
	IsConnected   bool          `json:"is_connected"`   // 连接状态
}