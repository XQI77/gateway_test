package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"gatesvr/internal/backup"
	"gatesvr/internal/config"
	"gatesvr/internal/gateway"
)

func main() {
	setupLogToFile()

	flags := parseFlags()
	if flags.showVersion {
		printVersion()
		os.Exit(0)
	}
	if flags.showHelp {
		printHelp()
		os.Exit(0)
	}

	serverConfig := buildServerConfig(flags)
	printStartupInfo(serverConfig)

	server := gateway.NewServer(serverConfig)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Start(ctx); err != nil {
		log.Fatalf("启动服务器失败: %v", err)
	}

	waitForShutdown(cancel, server)
}

type cliFlags struct {
	configFile        *string
	quicAddr          *string
	httpAddr          *string
	grpcAddr          *string
	metricsAddr       *string
	upstreamAddr      *string
	certFile          *string
	keyFile           *string
	sessionTimeout    *time.Duration
	ackTimeout        *time.Duration
	maxRetries        *int
	enableBackup      *bool
	backupMode        *string
	peerAddr          *string
	serverID          *string
	heartbeatInterval *time.Duration
	syncBatchSize     *int
	syncTimeout       *time.Duration
	readonly          *bool
	showVersion       bool
	showHelp          bool
}

func parseFlags() *cliFlags {
	flags := &cliFlags{
		configFile:        flag.String("config", "config.yaml", "配置文件路径"),
		quicAddr:          flag.String("quic", "", "QUIC监听地址"),
		httpAddr:          flag.String("http", "", "HTTP API监听地址"),
		grpcAddr:          flag.String("grpc", "", "gRPC服务监听地址"),
		metricsAddr:       flag.String("metrics", "", "监控指标监听地址"),
		upstreamAddr:      flag.String("upstream", "", "上游gRPC服务地址"),
		certFile:          flag.String("cert", "", "TLS证书文件路径"),
		keyFile:           flag.String("key", "", "TLS私钥文件路径"),
		sessionTimeout:    flag.Duration("session-timeout", 0, "会话超时时间"),
		ackTimeout:        flag.Duration("ack-timeout", 0, "ACK超时时间"),
		maxRetries:        flag.Int("max-retries", 0, "最大重试次数"),
		enableBackup:      flag.Bool("backup-enable", false, "启用热备份功能"),
		backupMode:        flag.String("backup-mode", "", "备份模式: primary或backup"),
		peerAddr:          flag.String("peer-addr", "", "对端服务器地址"),
		serverID:          flag.String("server-id", "", "服务器ID"),
		heartbeatInterval: flag.Duration("heartbeat-interval", 0, "心跳间隔"),
		syncBatchSize:     flag.Int("sync-batch-size", 0, "同步批次大小"),
		syncTimeout:       flag.Duration("sync-timeout", 0, "同步超时"),
		readonly:          flag.Bool("readonly", false, "只读模式"),
	}

	showVersion := flag.Bool("version", false, "显示版本信息")
	showHelp := flag.Bool("help", false, "显示帮助信息")

	flag.Parse()

	flags.showVersion = *showVersion
	flags.showHelp = *showHelp

	return flags
}

func buildServerConfig(flags *cliFlags) *gateway.Config {
	cfg, err := config.Load(*flags.configFile)
	if err != nil {
		log.Fatalf("加载配置文件失败: %v", err)
	}

	gatewayConfig, err := cfg.ToGatewayConfig()
	if err != nil {
		log.Fatalf("配置转换失败: %v", err)
	}

	applyCliOverrides(gatewayConfig, flags)
	applyEnvOverrides(gatewayConfig)
	applyBackupConfig(gatewayConfig, flags, cfg)

	if err := validateCertFiles(gatewayConfig.TLSCertFile, gatewayConfig.TLSKeyFile); err != nil {
		log.Fatalf("证书文件验证失败: %v", err)
	}

	return &gateway.Config{
		QUICAddr:                 gatewayConfig.QUICAddr,
		HTTPAddr:                 gatewayConfig.HTTPAddr,
		GRPCAddr:                 gatewayConfig.GRPCAddr,
		MetricsAddr:              gatewayConfig.MetricsAddr,
		UpstreamAddr:             gatewayConfig.UpstreamAddr,
		UpstreamServices:         gatewayConfig.UpstreamServices,
		TLSCertFile:              gatewayConfig.TLSCertFile,
		TLSKeyFile:               gatewayConfig.TLSKeyFile,
		SessionTimeout:           gatewayConfig.SessionTimeout,
		AckTimeout:               gatewayConfig.AckTimeout,
		MaxRetries:               gatewayConfig.MaxRetries,
		BackupConfig:             gatewayConfig.BackupConfig,
		ServerID:                 gatewayConfig.ServerID,
		OverloadProtectionConfig: convertOverloadConfig(gatewayConfig),
	}
}

func applyCliOverrides(cfg *config.GatewayConfig, flags *cliFlags) {
	if *flags.quicAddr != "" {
		cfg.QUICAddr = *flags.quicAddr
	}
	if *flags.httpAddr != "" {
		cfg.HTTPAddr = *flags.httpAddr
	}
	if *flags.grpcAddr != "" {
		cfg.GRPCAddr = *flags.grpcAddr
	}
	if *flags.metricsAddr != "" {
		cfg.MetricsAddr = *flags.metricsAddr
	}
	if *flags.upstreamAddr != "" {
		cfg.UpstreamAddr = *flags.upstreamAddr
	}
	if *flags.certFile != "" {
		cfg.TLSCertFile = *flags.certFile
	}
	if *flags.keyFile != "" {
		cfg.TLSKeyFile = *flags.keyFile
	}
	if *flags.sessionTimeout != 0 {
		cfg.SessionTimeout = *flags.sessionTimeout
	}
	if *flags.ackTimeout != 0 {
		cfg.AckTimeout = *flags.ackTimeout
	}
	if *flags.maxRetries != 0 {
		cfg.MaxRetries = *flags.maxRetries
	}
	if *flags.serverID != "" {
		cfg.ServerID = *flags.serverID
	}
}

func applyEnvOverrides(cfg *config.GatewayConfig) {
	if addr := os.Getenv("GATESVR_QUIC_ADDR"); addr != "" {
		cfg.QUICAddr = addr
	}
	if addr := os.Getenv("GATESVR_HTTP_ADDR"); addr != "" {
		cfg.HTTPAddr = addr
	}
	if addr := os.Getenv("GATESVR_GRPC_ADDR"); addr != "" {
		cfg.GRPCAddr = addr
	}
	if addr := os.Getenv("GATESVR_METRICS_ADDR"); addr != "" {
		cfg.MetricsAddr = addr
	}
	if addr := os.Getenv("GATESVR_UPSTREAM_ADDR"); addr != "" {
		cfg.UpstreamAddr = addr
	}
	if file := os.Getenv("GATESVR_CERT_FILE"); file != "" {
		cfg.TLSCertFile = file
	}
	if file := os.Getenv("GATESVR_KEY_FILE"); file != "" {
		cfg.TLSKeyFile = file
	}
}

func applyBackupConfig(cfg *config.GatewayConfig, flags *cliFlags, originalCfg *config.Config) {
	if !*flags.enableBackup && !originalCfg.Backup.Enabled {
		return
	}

	if cfg.BackupConfig == nil {
		cfg.BackupConfig = &backup.BackupConfig{
			Sync: backup.SyncConfig{
				Enabled:           true,
				Mode:              backup.ModePrimary,
				HeartbeatInterval: 2 * time.Second,
				SyncBatchSize:     50,
				SyncTimeout:       200 * time.Millisecond,
				BufferSize:        1000,
			},
			Failover: backup.FailoverConfig{
				DetectionTimeout: 6 * time.Second,
				SwitchTimeout:    10 * time.Second,
				RecoveryTimeout:  30 * time.Second,
				MaxRetries:       3,
			},
		}
	}

	if *flags.backupMode != "" {
		switch *flags.backupMode {
		case "primary":
			cfg.BackupConfig.Sync.Mode = backup.ModePrimary
		case "backup":
			cfg.BackupConfig.Sync.Mode = backup.ModeBackup
		default:
			log.Fatalf("无效的备份模式: %s", *flags.backupMode)
		}
	}

	if *flags.peerAddr != "" {
		cfg.BackupConfig.Sync.PeerAddr = *flags.peerAddr
	}
	if *flags.heartbeatInterval != 0 {
		cfg.BackupConfig.Sync.HeartbeatInterval = *flags.heartbeatInterval
		cfg.BackupConfig.Failover.DetectionTimeout = *flags.heartbeatInterval * 3
	}
	if *flags.syncBatchSize != 0 {
		cfg.BackupConfig.Sync.SyncBatchSize = *flags.syncBatchSize
	}
	if *flags.syncTimeout != 0 {
		cfg.BackupConfig.Sync.SyncTimeout = *flags.syncTimeout
	}
	if *flags.readonly {
		cfg.BackupConfig.Sync.ReadOnly = *flags.readonly
	}

	if cfg.BackupConfig.Sync.PeerAddr == "" {
		log.Fatalf("启用备份功能时必须指定对端地址")
	}
}

func convertOverloadConfig(cfg *config.GatewayConfig) *gateway.OverloadConfig {
	if cfg.OverloadProtection == nil {
		return nil
	}

	return &gateway.OverloadConfig{
		Enabled:                    cfg.OverloadProtection.Enabled,
		MaxConnections:             cfg.OverloadProtection.MaxConnections,
		ConnectionWarningThreshold: cfg.OverloadProtection.ConnectionWarningThreshold,
		MaxQPS:                     cfg.OverloadProtection.MaxQPS,
		QPSWarningThreshold:        cfg.OverloadProtection.QPSWarningThreshold,
		QPSWindowSeconds:           cfg.OverloadProtection.QPSWindowSeconds,
		MaxUpstreamConcurrent:      cfg.OverloadProtection.MaxUpstreamConcurrent,
		UpstreamTimeout:            cfg.OverloadProtection.UpstreamTimeout,
		UpstreamWarningThreshold:   cfg.OverloadProtection.UpstreamWarningThreshold,
	}
}

func waitForShutdown(cancel context.CancelFunc, server *gateway.Server) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("网关服务器正在运行...")
	<-sigCh

	log.Printf("收到停止信号，正在关闭服务器...")
	cancel()
	server.Stop()
	log.Printf("服务器已停止")
}

func printVersion() {
	fmt.Println("网关服务器 v1.0.0")
	fmt.Println("构建时间:", getBuildTime())
}

func printHelp() {
	fmt.Println("网关服务器 - 高性能QUIC网关")
	fmt.Println()
	fmt.Println("使用方法:")
	flag.PrintDefaults()
}

func validateCertFiles(certFile, keyFile string) error {
	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		return fmt.Errorf("证书文件不存在: %s", certFile)
	}
	if _, err := os.Stat(keyFile); os.IsNotExist(err) {
		return fmt.Errorf("私钥文件不存在: %s", keyFile)
	}
	return nil
}

func printStartupInfo(config *gateway.Config) {
	fmt.Println("========================================")
	fmt.Println("         网关服务器 v1.0.0")
	fmt.Println("========================================")
	fmt.Printf("QUIC地址:     %s\n", config.QUICAddr)
	fmt.Printf("HTTP API:     %s\n", config.HTTPAddr)
	fmt.Printf("监控指标:     %s\n", config.MetricsAddr)
	fmt.Printf("上游服务:     %s\n", config.UpstreamAddr)
	fmt.Printf("TLS证书:      %s\n", config.TLSCertFile)
	fmt.Printf("TLS私钥:      %s\n", config.TLSKeyFile)
	fmt.Printf("会话超时:     %v\n", config.SessionTimeout)
	fmt.Printf("ACK超时:      %v\n", config.AckTimeout)
	fmt.Printf("最大重试:     %d\n", config.MaxRetries)
	fmt.Println("========================================")
	fmt.Println()

	fmt.Println("可用的API端点:")
	fmt.Printf("  健康检查:   http://%s/health\n", config.HTTPAddr)
	fmt.Printf("  会话统计:   http://%s/stats\n", config.HTTPAddr)
	fmt.Printf("  性能监控:   http://%s/performance\n", config.HTTPAddr)
	fmt.Printf("  监控指标:   http://%s/metrics\n", config.MetricsAddr)
	fmt.Println()
}

func setupLogToFile() {
	logFile := "gatesvr.log"
	writer := &rotatingWriter{
		filename:  logFile,
		maxSize:   3 * 1024 * 1024,
		maxBackup: 5,
	}

	if err := writer.openCurrent(); err != nil {
		fmt.Printf("无法创建日志文件 %s: %v\n", logFile, err)
		os.Exit(1)
	}

	log.SetOutput(writer)
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	fmt.Printf("日志已配置输出到文件: %s (最大3MB)\n", logFile)
}

type rotatingWriter struct {
	filename    string
	maxSize     int64
	maxBackup   int
	file        *os.File
	currentSize int64
}

func (w *rotatingWriter) openCurrent() error {
	if info, err := os.Stat(w.filename); err == nil {
		w.currentSize = info.Size()
		if w.currentSize >= w.maxSize {
			if err := w.rotate(); err != nil {
				return err
			}
			w.currentSize = 0
		}
	}

	file, err := os.OpenFile(w.filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return err
	}

	w.file = file
	return nil
}

func (w *rotatingWriter) Write(p []byte) (n int, err error) {
	if w.currentSize+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}

	n, err = w.file.Write(p)
	if err != nil {
		return n, err
	}

	w.currentSize += int64(n)
	return n, nil
}

func (w *rotatingWriter) rotate() error {
	if w.file != nil {
		w.file.Close()
	}

	for i := w.maxBackup - 1; i >= 1; i-- {
		oldName := w.filename + "." + strconv.Itoa(i)
		newName := w.filename + "." + strconv.Itoa(i+1)

		if _, err := os.Stat(oldName); err == nil {
			if i == w.maxBackup-1 {
				os.Remove(newName)
			}
			os.Rename(oldName, newName)
		}
	}

	if _, err := os.Stat(w.filename); err == nil {
		backupName := w.filename + ".1"
		if err := os.Rename(w.filename, backupName); err != nil {
			return err
		}
	}

	file, err := os.OpenFile(w.filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}

	w.file = file
	w.currentSize = 0
	return nil
}

func (w *rotatingWriter) Close() error {
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

func getBuildTime() string {
	return "unknown"
}
