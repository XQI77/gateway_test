// Package gateway 提供网关服务器配置管理
package gateway

import (
	"time"

	"gatesvr/internal/backup"
)

// Config 网关服务器配置
type Config struct {
	// 服务地址配置
	QUICAddr         string
	HTTPAddr         string
	GRPCAddr         string
	MetricsAddr      string
	UpstreamAddr     string
	UpstreamServices map[string][]string

	// TLS配置
	TLSCertFile string //
	TLSKeyFile  string //

	// 会话配置
	SessionTimeout time.Duration
	AckTimeout     time.Duration
	MaxRetries     int

	// 备份配置
	BackupConfig *backup.BackupConfig
	ServerID     string

	// 过载保护配置
	OverloadProtectionConfig *OverloadConfig

	// 异步处理配置
	AsyncConfig *AsyncConfig
}
