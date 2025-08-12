// Package gateway 提供网关服务器核心功能
package gateway

import (
	"context"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof" // 导入pprof HTTP端点
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"google.golang.org/grpc"

	"gatesvr/internal/backup"
	"gatesvr/internal/message"
	"gatesvr/internal/session"
	"gatesvr/internal/upstream"
	"gatesvr/pkg/metrics"
	pb "gatesvr/proto"
)

// Server 网关服务器
type Server struct {
	config *Config

	// 嵌入gRPC服务接口
	pb.UnimplementedGatewayServiceServer

	// 核心组件
	sessionManager     *session.Manager
	messageCodec       *message.MessageCodec
	metrics            *metrics.GateServerMetrics
	performanceTracker *SimpleTracker
	orderedSender      *OrderedMessageSender

	// 上游服务管理
	upstreamServices *upstream.UpstreamServices //
	upstreamManager  *upstream.ServiceManager   // 上游服务连接管理器

	// 服务器实例
	quicListener  *quic.Listener
	httpServer    *http.Server
	metricsServer *metrics.MetricsServer
	grpcServer    *grpc.Server

	// 备份管理器(暂时没做好)
	backupManager backup.BackupManager

	// 过载保护器
	overloadProtector *OverloadProtector

	// 异步处理器
	asyncProcessor *AsyncRequestProcessor

	running      bool
	runningMutex sync.RWMutex
	stopCh       chan struct{}
	wg           sync.WaitGroup
}

// 创建新的网关服务器
func NewServer(config *Config) *Server {
	server := &Server{
		config:             config,
		sessionManager:     session.NewManager(config.SessionTimeout, config.AckTimeout, config.MaxRetries),
		messageCodec:       message.NewMessageCodec(),
		metrics:            metrics.NewGateServerMetrics(),
		performanceTracker: NewSimpleTracker(),
		upstreamServices:   upstream.NewUpstreamServices(),
		stopCh:             make(chan struct{}),
	}

	// 初始化有序消息发送器
	server.orderedSender = NewOrderedMessageSender(server)

	// 初始化备份管理器
	if config.BackupConfig != nil && config.BackupConfig.Sync.Enabled {
		server.backupManager = backup.NewBackupManager(config.BackupConfig, config.ServerID)
		server.backupManager.RegisterSessionManager(server.sessionManager)

		// 设置会话管理器的同步回调
		server.sessionManager.EnableSync(server.onSessionSync)

		log.Printf("备份管理器已初始化 - 服务器ID: %s, 模式: %d", config.ServerID, config.BackupConfig.Sync.Mode)
	}

	// 初始化过载保护器
	server.overloadProtector = NewOverloadProtector(config.OverloadProtectionConfig, server.metrics)

	// 初始化异步处理器
	asyncConfig := config.AsyncConfig
	if asyncConfig == nil {
		asyncConfig = DefaultAsyncConfig
	}
	server.asyncProcessor = NewAsyncRequestProcessor(server, asyncConfig)

	// 初始化上游服务配置
	server.initUpstreamServices()

	return server
}

// initUpstreamServices 初始化上游服务配置
func (s *Server) initUpstreamServices() {
	// 从配置中添加上游服务
	for serviceName, addresses := range s.config.UpstreamServices {
		serviceType := upstream.ServiceType(serviceName)
		s.upstreamServices.AddService(serviceType, addresses)
		log.Printf("已配置上游服务: %s -> %v", serviceName, addresses)
	}
}

// 启动网关服务器
func (s *Server) Start(ctx context.Context) error {
	log.Printf("启动网关服务器...")

	s.runningMutex.Lock()
	s.running = true
	s.runningMutex.Unlock()

	// 初始化上游服务连接
	if err := s.initUpstreamConnections(); err != nil {
		return fmt.Errorf("初始化上游服务连接失败: %w", err)
	}

	// 启动各种服务器
	if err := s.startQUICListener(); err != nil {
		return fmt.Errorf("启动QUIC监听器失败: %w", err)
	}

	if err := s.startHTTPServer(); err != nil {
		return fmt.Errorf("启动HTTP服务器失败: %w", err)
	}

	if err := s.startMetricsServer(); err != nil {
		return fmt.Errorf("启动监控服务器失败: %w", err)
	}

	if err := s.startGRPCServer(); err != nil {
		return fmt.Errorf("启动gRPC服务器失败: %w", err)
	}

	// 启动会话管理器
	s.sessionManager.Start(ctx)

	// 启动备份管理器
	if s.backupManager != nil {
		if err := s.backupManager.Start(ctx); err != nil {
			log.Printf("启动备份管理器失败: %v", err)
		} else {
			log.Printf("备份管理器已启动")
		}
	}

	// 启动异步处理器
	if s.asyncProcessor != nil {
		if err := s.asyncProcessor.Start(); err != nil {
			log.Printf("启动异步处理器失败: %v", err)
		} else {
			log.Printf("异步处理器已启动")
		}
	}

	// 启动连接处理器
	s.wg.Add(1)
	go s.acceptConnections(ctx)

	log.Printf("网关服务器启动完成")
	log.Printf("QUIC监听地址: %s", s.config.QUICAddr)
	log.Printf("HTTP监听地址: %s", s.config.HTTPAddr)
	log.Printf("gRPC监听地址: %s", s.config.GRPCAddr)
	log.Printf("监控地址: %s", s.config.MetricsAddr)
	log.Printf("上游服务地址: %s", s.config.UpstreamAddr)

	return nil
}

func (s *Server) Stop() {
	log.Printf("正在停止网关服务器...")

	s.runningMutex.Lock()
	s.running = false
	s.runningMutex.Unlock()

	close(s.stopCh)

	if s.quicListener != nil {
		s.quicListener.Close()
	}

	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.httpServer.Shutdown(ctx)
		cancel()
	}

	if s.metricsServer != nil {
		s.metricsServer.Stop()
	}

	if s.grpcServer != nil {
		s.grpcServer.GracefulStop()
	}

	if s.asyncProcessor != nil {
		if err := s.asyncProcessor.Stop(); err != nil {
			log.Printf("停止异步处理器失败: %v", err)
		} else {
			log.Printf("异步处理器已停止")
		}
	}

	if s.backupManager != nil {
		if err := s.backupManager.Stop(); err != nil {
			log.Printf("停止备份管理器失败: %v", err)
		} else {
			log.Printf("备份管理器已停止")
		}
	}
	s.sessionManager.Stop()

	s.wg.Wait()

	log.Printf("网关服务器已停止")
}

// 会话同步回调函数
func (s *Server) onSessionSync(sessionID string, session *session.Session, event string) {
	if s.backupManager == nil {
		return
	}

	switch event {
	case "session_created", "session_reconnected", "session_activated":
		// 同步会话数据
		log.Printf("同步会话事件: %s - 会话: %s", event, sessionID)
		// 这里可以调用备份管理器的同步方法
		// 由于BackupManager接口没有直接的同步方法，这里暂时记录日志

	case "session_deleted":
		// 同步会话删除
		log.Printf("同步会话删除: %s", sessionID)

	default:
		log.Printf("未知会话同步事件: %s - 会话: %s", event, sessionID)
	}
}

func (s *Server) GetBackupStats() map[string]interface{} {
	if s.backupManager == nil {
		return map[string]interface{}{
			"backup_enabled": false,
		}
	}

	stats := s.backupManager.GetStats()
	stats["backup_enabled"] = true
	return stats
}

// 切换备份模式
func (s *Server) SwitchBackupMode(mode backup.ServerMode) error {
	if s.backupManager == nil {
		return fmt.Errorf("备份功能未启用")
	}

	return s.backupManager.SwitchMode(mode)
}

// 手动触发备份同步
func (s *Server) TriggerBackupSync() error {
	if s.backupManager == nil {
		return fmt.Errorf("备份功能未启用")
	}

	return s.backupManager.TriggerSync()
}

// 手动触发故障切换
func (s *Server) TriggerFailover() error {
	if s.backupManager == nil {
		return fmt.Errorf("备份功能未启用")
	}

	return s.backupManager.TriggerFailover()
}
