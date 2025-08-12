package gateway

import (
	"crypto/tls"
	"fmt"
	"gatesvr/internal/upstream"
	"gatesvr/pkg/metrics"
	pb "gatesvr/proto"
	"log"
	"net"
	"net/http"

	"github.com/quic-go/quic-go"
	"google.golang.org/grpc"
)

// 启动QUIC监听器
func (s *Server) startQUICListener() error {
	// 加载TLS证书
	cert, err := tls.LoadX509KeyPair(s.config.TLSCertFile, s.config.TLSKeyFile)
	if err != nil {
		return fmt.Errorf("加载TLS证书失败: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"gatesvr"},
	}

	// 创建QUIC监听器
	listener, err := quic.ListenAddr(s.config.QUICAddr, tlsConfig, nil)
	if err != nil {
		return fmt.Errorf("创建QUIC监听器失败: %w", err)
	}

	s.quicListener = listener
	log.Printf("QUIC监听器已启动: %s", s.config.QUICAddr)
	return nil
}

// 启动HTTP API服务器
func (s *Server) startHTTPServer() error {
	mux := http.NewServeMux()

	// 基础监控API（保留）
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/stats", s.handleStats)

	// Go pprof性能分析端点（替换复杂的性能监控）
	mux.HandleFunc("/debug/pprof/", http.DefaultServeMux.ServeHTTP) // pprof索引页
	mux.HandleFunc("/pprof/", http.DefaultServeMux.ServeHTTP)       // 简化路径

	// 可选：简化性能监控端点（如果需要基本统计）
	mux.HandleFunc("/performance", s.handleSimplePerformance)

	s.httpServer = &http.Server{
		Addr:    s.config.HTTPAddr,
		Handler: mux,
	}

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP服务器错误: %v", err)
		}
	}()

	log.Printf("HTTP服务器已启动: %s", s.config.HTTPAddr)
	return nil
}

func (s *Server) startMetricsServer() error {
	s.metricsServer = metrics.NewMetricsServer(s.config.MetricsAddr)

	go func() {
		if err := s.metricsServer.Start(); err != nil && err != http.ErrServerClosed {
			log.Printf("监控服务器错误: %v", err)
		}
	}()

	log.Printf("监控服务器已启动: %s", s.config.MetricsAddr)
	return nil
}

// 启动gRPC服务器供上游服务调用
func (s *Server) startGRPCServer() error {
	listener, err := net.Listen("tcp", s.config.GRPCAddr)
	if err != nil {
		return fmt.Errorf("gRPC监听失败: %w", err)
	}

	grpcServer := grpc.NewServer()

	// 注册GatewayService
	pb.RegisterGatewayServiceServer(grpcServer, s)
	go func() {
		log.Printf("gRPC服务器已启动: %s", s.config.GRPCAddr)
		if err := grpcServer.Serve(listener); err != nil {
			log.Printf("gRPC服务器错误: %v", err)
		}
	}()

	s.grpcServer = grpcServer

	return nil
}

func (s *Server) isRunning() bool {
	s.runningMutex.RLock()
	defer s.runningMutex.RUnlock()
	return s.running
}

func (s *Server) initUpstreamConnections() error {
	// 如果没有配置多上游服务，尝试向后兼容模式
	if s.upstreamServices.GetServiceCount() == 0 && s.config.UpstreamAddr != "" {
		// 向后兼容：连接单一上游服务
		log.Printf("上游服务启动失败 ")
		return nil //（todo不能返回nil）
	}

	// 创建上游服务管理器
	s.upstreamManager = upstream.NewServiceManager(s.upstreamServices)

	// 预连接所有配置的上游服务
	services := s.upstreamServices.GetAllServices()
	connectedCount := 0

	for serviceType := range services {
		if s.upstreamServices.IsServiceAvailable(serviceType) {
			// 尝试预连接服务（不阻塞启动）
			go func(st upstream.ServiceType) {
				if _, err := s.upstreamManager.GetClient(st); err != nil {
					log.Printf("警告: 预连接上游服务失败 - %s: %v", st, err)
				} else {
					log.Printf("预连接上游服务成功 - %s", st)
				}
			}(serviceType)
			connectedCount++
		}
	}

	if connectedCount == 0 {
		return fmt.Errorf("没有可用的上游服务")
	}

	log.Printf("已初始化上游服务连接管理器 - 配置服务数: %d", connectedCount)
	return nil
}
