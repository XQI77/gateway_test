package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"gatesvr/internal/upstream"
)

// main Zone路由上游服务启动器 - 支持gRPC业务处理、自动注册、健康检查
func main() {
	var (
		addr        = flag.String("addr", ":9000", "gRPC服务监听地址")
		zoneID      = flag.String("zone", "", "Zone ID (001-006, 必填)")
		gateway     = flag.String("gateway", "localhost:8092", "Gateway gRPC地址用于注册")
		healthCheck = flag.Bool("health-check", false, "仅执行健康检查")
		showVersion = flag.Bool("version", false, "显示版本信息")
		showHelp    = flag.Bool("help", false, "显示帮助信息")
	)

	flag.Parse()

	if *healthCheck {
		if err := performHealthCheck(*addr); err != nil {
			log.Printf("健康检查失败: %v", err)
			os.Exit(1)
		}
		log.Printf("健康检查成功")
		os.Exit(0)
	}

	if *showVersion {
		fmt.Println("上游服务 v1.0.0")
		fmt.Println("构建时间:", getBuildTime())
		os.Exit(0)
	}

	if *showHelp {
		fmt.Println("上游服务 - 基于Zone的gRPC业务逻辑服务器")
		fmt.Println("\n使用方法:")
		flag.PrintDefaults()
		fmt.Println("\n示例:")
		fmt.Println("  ./upstream --zone=001 --addr=:9001 --gateway=localhost:8092")
		fmt.Println("\n环境变量: UPSTREAM_ADDR, UPSTREAM_ZONE, GATEWAY_ADDR")
		fmt.Println("\nZone分配: 001-006 对应不同OpenID范围")
		fmt.Println("\n业务操作: echo, time, hello, calculate, status")
		os.Exit(0)
	}

	if envAddr := os.Getenv("UPSTREAM_ADDR"); envAddr != "" {
		*addr = envAddr
	}
	if envZone := os.Getenv("UPSTREAM_ZONE"); envZone != "" {
		*zoneID = envZone
	}
	if envGateway := os.Getenv("GATEWAY_ADDR"); envGateway != "" {
		*gateway = envGateway
	}

	if *zoneID == "" {
		log.Fatal("Zone ID是必填参数，请使用 --zone 指定 (001-006)")
	}
	if !isValidZoneID(*zoneID) {
		log.Fatalf("无效的Zone ID: %s，必须是001-006之间的三位数字", *zoneID)
	}

	printStartupInfo(*addr, *zoneID, *gateway)

	server := upstream.NewServer(*addr)
	server.SetZoneInfo(*zoneID, *gateway)

	go startHealthServer(*addr)

	if err := server.Start(); err != nil {
		log.Fatalf("启动上游服务失败: %v", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("上游服务正在运行... (按Ctrl+C停止)")
	<-sigCh

	log.Printf("收到停止信号，正在关闭服务...")
	server.Stop()
	log.Printf("上游服务已停止")
}

func printStartupInfo(addr, zoneID, gateway string) {
	fmt.Println("========================================")
	fmt.Println("      上游服务 v1.0.0 (Zone模式)")
	fmt.Println("========================================")
	fmt.Printf("gRPC地址:     %s\n", addr)
	fmt.Printf("Zone ID:      %s\n", zoneID)
	fmt.Printf("Gateway地址:  %s\n", gateway)
	fmt.Println("========================================")
	fmt.Printf("业务操作: echo, time, hello, calculate, status\n")
	fmt.Printf("gRPC接口: ProcessRequest, GetStatus\n\n")
}

func getBuildTime() string {
	return "unknown"
}

// startHealthServer HTTP健康检查服务 - 端口 = gRPC端口 + 1000
func startHealthServer(grpcAddr string) {
	grpcPort := extractPort(grpcAddr)
	healthPort := grpcPort + 1000
	httpAddr := fmt.Sprintf(":%d", healthPort)
	
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	
	log.Printf("启动HTTP健康检查服务器: %s (gRPC端口: %d)", httpAddr, grpcPort)
	
	if err := http.ListenAndServe(httpAddr, mux); err != nil {
		log.Printf("HTTP健康检查服务器启动失败: %v", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "healthy", "service": "upstream"}`))
}

func performHealthCheck(addr string) error {
	grpcPort := extractPort(addr)
	healthPort := grpcPort + 1000
	healthURL := fmt.Sprintf("http://localhost:%d/health", healthPort)
	
	resp, err := http.Get(healthURL)
	if err != nil {
		return fmt.Errorf("请求健康检查端点失败: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("健康检查返回状态码: %d", resp.StatusCode)
	}
	
	return nil
}

func extractPort(addr string) int {
	if strings.HasPrefix(addr, ":") {
		if port, err := strconv.Atoi(addr[1:]); err == nil {
			return port
		}
	}
	
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		if port, err := strconv.Atoi(addr[idx+1:]); err == nil {
			return port
		}
	}
	
	return 8081
}

// isValidZoneID 验证Zone ID格式 (001-006)
func isValidZoneID(zoneID string) bool {
	if len(zoneID) != 3 {
		return false
	}
	
	for _, char := range zoneID {
		if char < '0' || char > '9' {
			return false
		}
	}
	
	zone, err := strconv.Atoi(zoneID)
	if err != nil {
		return false
	}
	
	return zone >= 1 && zone <= 6
}
