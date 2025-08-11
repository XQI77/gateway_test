// Package config 提供配置文件读取功能
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
	"gatesvr/internal/backup"
)

// Config 应用程序配置
type Config struct {
	Server            ServerConfig            `yaml:"server"`
	StartProcessor    StartProcessorConfig    `yaml:"start_processor"`
	Backup            BackupConfig            `yaml:"backup"`
	OverloadProtection OverloadProtectionConfig `yaml:"overload_protection"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	QUICAddr       string                    `yaml:"quic_addr"`
	HTTPAddr       string                    `yaml:"http_addr"`
	GRPCAddr       string                    `yaml:"grpc_addr"`
	MetricsAddr    string                    `yaml:"metrics_addr"`
	UpstreamAddr   string                    `yaml:"upstream_addr"`   // 保留向后兼容
	UpstreamServices map[string][]string     `yaml:"upstream_services"` // 新的多上游配置
	CertFile       string                    `yaml:"cert_file"`
	KeyFile        string                    `yaml:"key_file"`
	SessionTimeout string                    `yaml:"session_timeout"`
	AckTimeout     string                    `yaml:"ack_timeout"`
	MaxRetries     int                       `yaml:"max_retries"`
}

// StartProcessorConfig START消息处理器配置
type StartProcessorConfig struct {
	Enabled    bool   `yaml:"enabled"`
	MaxWorkers int    `yaml:"max_workers"`
	QueueSize  int    `yaml:"queue_size"`
	Timeout    string `yaml:"timeout"`
}

// HeartbeatConfig 心跳配置
type HeartbeatConfig struct {
	Interval  string `yaml:"interval"`    // 心跳间隔
	PeerAddr  string `yaml:"peer_addr"`   // 主服务器连接目标地址
	ListenAddr string `yaml:"listen_addr"` // 备份服务器监听地址
	Timeout   string `yaml:"timeout"`     // 心跳超时时间
}

// SyncConfig 同步配置
type SyncConfig struct {
	PeerAddr   string `yaml:"peer_addr"`    // 主服务器连接目标地址
	ListenAddr string `yaml:"listen_addr"`  // 备份服务器监听地址
	BatchSize  int    `yaml:"batch_size"`   // 同步批次大小
	Timeout    string `yaml:"timeout"`      // 同步超时
	BufferSize int    `yaml:"buffer_size"`  // 同步缓冲区大小
}

// BackupConfig 备份配置
type BackupConfig struct {
	Enabled   bool            `yaml:"enabled"`
	Mode      string          `yaml:"mode"`
	ServerID  string          `yaml:"server_id"`
	ReadOnly  bool            `yaml:"readonly"`
	Heartbeat HeartbeatConfig `yaml:"heartbeat"`
	Sync      SyncConfig      `yaml:"sync"`
}

// OverloadProtectionConfig 过载保护配置
type OverloadProtectionConfig struct {
	Enabled                      bool   `yaml:"enabled"`
	MaxConnections              int    `yaml:"max_connections"`
	ConnectionWarningThreshold   int    `yaml:"connection_warning_threshold"`
	MaxQPS                      int    `yaml:"max_qps"`
	QPSWarningThreshold         int    `yaml:"qps_warning_threshold"`
	QPSWindowSeconds            int    `yaml:"qps_window_seconds"`
	MaxUpstreamConcurrent       int    `yaml:"max_upstream_concurrent"`
	UpstreamTimeout             string `yaml:"upstream_timeout"`
	UpstreamWarningThreshold    int    `yaml:"upstream_warning_threshold"`
}

// Load 从文件加载配置
func Load(configFile string) (*Config, error) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	return &config, nil
}

// ParseDuration 解析时间字符串
func (c *Config) ParseDuration(durationStr string) (time.Duration, error) {
	return time.ParseDuration(durationStr)
}

// ToGatewayConfig 转换为网关配置
func (c *Config) ToGatewayConfig() (*GatewayConfig, error) {
	sessionTimeout, err := c.ParseDuration(c.Server.SessionTimeout)
	if err != nil {
		return nil, fmt.Errorf("解析会话超时时间失败: %w", err)
	}

	ackTimeout, err := c.ParseDuration(c.Server.AckTimeout)
	if err != nil {
		return nil, fmt.Errorf("解析ACK超时时间失败: %w", err)
	}

	config := &GatewayConfig{
		QUICAddr:         c.Server.QUICAddr,
		HTTPAddr:         c.Server.HTTPAddr,
		GRPCAddr:         c.Server.GRPCAddr,
		MetricsAddr:      c.Server.MetricsAddr,
		UpstreamAddr:     c.Server.UpstreamAddr,
		UpstreamServices: c.Server.UpstreamServices,
		TLSCertFile:      c.Server.CertFile,
		TLSKeyFile:       c.Server.KeyFile,
		SessionTimeout:   sessionTimeout,
		AckTimeout:       ackTimeout,
		MaxRetries:       c.Server.MaxRetries,
		ServerID:         c.Backup.ServerID,
	}

	// 处理 StartProcessor 配置
	if c.StartProcessor.Enabled || c.StartProcessor.MaxWorkers > 0 {
		timeout := 30 * time.Second // 默认值
		if c.StartProcessor.Timeout != "" {
			if parsed, err := c.ParseDuration(c.StartProcessor.Timeout); err == nil {
				timeout = parsed
			}
		}

		config.StartProcessor = &StartProcessorGatewayConfig{
			Enabled:    c.StartProcessor.Enabled,
			MaxWorkers: c.StartProcessor.MaxWorkers,
			QueueSize:  c.StartProcessor.QueueSize,
			Timeout:    timeout,
		}

		// 设置默认值
		if config.StartProcessor.MaxWorkers == 0 {
			config.StartProcessor.MaxWorkers = 100
		}
		if config.StartProcessor.QueueSize == 0 {
			config.StartProcessor.QueueSize = 1000
		}
	}

	// 处理备份配置
	if c.Backup.Enabled {
		// 解析心跳间隔
		heartbeatInterval, err := c.ParseDuration(c.Backup.Heartbeat.Interval)
		if err != nil {
			return nil, fmt.Errorf("解析心跳间隔失败: %w", err)
		}

		// 解析心跳超时
		heartbeatTimeout, err := c.ParseDuration(c.Backup.Heartbeat.Timeout)
		if err != nil {
			return nil, fmt.Errorf("解析心跳超时失败: %w", err)
		}

		// 解析同步超时
		syncTimeout, err := c.ParseDuration(c.Backup.Sync.Timeout)
		if err != nil {
			return nil, fmt.Errorf("解析同步超时失败: %w", err)
		}

		// 解析备份模式
		var mode backup.ServerMode
		switch c.Backup.Mode {
		case "primary":
			mode = backup.ModePrimary
		case "backup":
			mode = backup.ModeBackup
		default:
			return nil, fmt.Errorf("无效的备份模式: %s, 支持: primary, backup", c.Backup.Mode)
		}

		// 确定心跳和同步地址
		var heartbeatAddr, syncAddr string
		if mode == backup.ModePrimary {
			// 主服务器使用 peer_addr 连接到备份服务器
			heartbeatAddr = c.Backup.Heartbeat.PeerAddr
			syncAddr = c.Backup.Sync.PeerAddr
		} else {
			// 备份服务器使用 listen_addr 监听主服务器连接
			heartbeatAddr = c.Backup.Heartbeat.ListenAddr
			syncAddr = c.Backup.Sync.ListenAddr
		}

		config.BackupConfig = &backup.BackupConfig{
			Sync: backup.SyncConfig{
				Enabled:           c.Backup.Enabled,
				Mode:              mode,
				PeerAddr:          heartbeatAddr, // 心跳地址（临时兼容，后续会分离）
				HeartbeatInterval: heartbeatInterval,
				SyncBatchSize:     c.Backup.Sync.BatchSize,
				SyncTimeout:       syncTimeout,
				ReadOnly:          c.Backup.ReadOnly,
				BufferSize:        c.Backup.Sync.BufferSize,
			},
			Failover: backup.FailoverConfig{
				DetectionTimeout: heartbeatTimeout,
				SwitchTimeout:    10 * time.Second,
				RecoveryTimeout:  30 * time.Second,
				MaxRetries:       3,
			},
		}

		// 添加新的字段到配置中（为了向后兼容和新逻辑）
		config.BackupConfig.HeartbeatAddr = heartbeatAddr
		config.BackupConfig.SyncAddr = syncAddr
	}

	// 处理过载保护配置
	if c.OverloadProtection.Enabled || c.OverloadProtection.MaxConnections > 0 {
		upstreamTimeout := 30 * time.Second // 默认值
		if c.OverloadProtection.UpstreamTimeout != "" {
			if parsed, err := c.ParseDuration(c.OverloadProtection.UpstreamTimeout); err == nil {
				upstreamTimeout = parsed
			}
		}

		config.OverloadProtection = &OverloadProtectionGatewayConfig{
			Enabled:                      c.OverloadProtection.Enabled,
			MaxConnections:              c.OverloadProtection.MaxConnections,
			ConnectionWarningThreshold:   c.OverloadProtection.ConnectionWarningThreshold,
			MaxQPS:                      c.OverloadProtection.MaxQPS,
			QPSWarningThreshold:         c.OverloadProtection.QPSWarningThreshold,
			QPSWindowSeconds:            c.OverloadProtection.QPSWindowSeconds,
			MaxUpstreamConcurrent:       c.OverloadProtection.MaxUpstreamConcurrent,
			UpstreamTimeout:             upstreamTimeout,
			UpstreamWarningThreshold:    c.OverloadProtection.UpstreamWarningThreshold,
		}

		// 设置默认值
		if config.OverloadProtection.MaxConnections == 0 {
			config.OverloadProtection.MaxConnections = 1000
		}
		if config.OverloadProtection.ConnectionWarningThreshold == 0 {
			config.OverloadProtection.ConnectionWarningThreshold = 800
		}
		if config.OverloadProtection.MaxQPS == 0 {
			config.OverloadProtection.MaxQPS = 2000
		}
		if config.OverloadProtection.QPSWarningThreshold == 0 {
			config.OverloadProtection.QPSWarningThreshold = 1600
		}
		if config.OverloadProtection.QPSWindowSeconds == 0 {
			config.OverloadProtection.QPSWindowSeconds = 10
		}
		if config.OverloadProtection.MaxUpstreamConcurrent == 0 {
			config.OverloadProtection.MaxUpstreamConcurrent = 100
		}
		if config.OverloadProtection.UpstreamWarningThreshold == 0 {
			config.OverloadProtection.UpstreamWarningThreshold = 80
		}
	}

	return config, nil
}

// GatewayConfig 网关配置（兼容现有接口）
type GatewayConfig struct {
	QUICAddr            string
	HTTPAddr            string
	GRPCAddr            string
	MetricsAddr         string
	UpstreamAddr        string                        // 保留向后兼容
	UpstreamServices    map[string][]string           // 新的多上游配置
	TLSCertFile         string
	TLSKeyFile          string
	SessionTimeout      time.Duration
	AckTimeout          time.Duration
	MaxRetries          int
	StartProcessor      *StartProcessorGatewayConfig
	BackupConfig        *backup.BackupConfig
	ServerID            string
	OverloadProtection  *OverloadProtectionGatewayConfig
}

// StartProcessorGatewayConfig START处理器网关配置
type StartProcessorGatewayConfig struct {
	Enabled    bool
	MaxWorkers int
	QueueSize  int
	Timeout    time.Duration
}

// OverloadProtectionGatewayConfig 网关过载保护配置
type OverloadProtectionGatewayConfig struct {
	Enabled                      bool
	MaxConnections              int
	ConnectionWarningThreshold   int
	MaxQPS                      int
	QPSWarningThreshold         int
	QPSWindowSeconds            int
	MaxUpstreamConcurrent       int
	UpstreamTimeout             time.Duration
	UpstreamWarningThreshold    int
}