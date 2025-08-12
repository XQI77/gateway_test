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

func main() {

	var (
		addr        = flag.String("addr", ":9000", "gRPC服务监听地址")
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
		fmt.Println("上游服务 - gRPC业务逻辑服务器")
		fmt.Println()
		fmt.Println("使用方法:")
		flag.PrintDefaults()
		fmt.Println()
		fmt.Println("示例:")
		fmt.Println("  ./upstream -addr :9000")
		fmt.Println()
		fmt.Println("环境变量:")
		fmt.Println("  UPSTREAM_ADDR    gRPC监听地址")
		fmt.Println()
		fmt.Println("支持的业务操作:")
		fmt.Println("  echo       - 回显消息")
		fmt.Println("  time       - 获取当前时间")
		fmt.Println("  hello      - 问候消息")
		fmt.Println("  calculate  - 数学计算")
		fmt.Println("  status     - 服务状态查询")
		os.Exit(0)
	}

	// 从环境变量读取配置
	if envAddr := os.Getenv("UPSTREAM_ADDR"); envAddr != "" {
		*addr = envAddr
	}

	printStartupInfo(*addr)

	server := upstream.NewServer(*addr)

	go startHealthServer(*addr)

	// 启动服务器
	if err := server.Start(); err != nil {
		log.Fatalf("启动上游服务失败: %v", err)
	}

	// 等待中断信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("上游服务正在运行... (按Ctrl+C停止)")
	<-sigCh

	log.Printf("收到停止信号，正在关闭服务...")

	// 停止服务器
	server.Stop()

	log.Printf("上游服务已停止")
}

func printStartupInfo(addr string) {
	fmt.Println("========================================")
	fmt.Println("         上游服务 v1.0.0")
	fmt.Println("========================================")
	fmt.Printf("gRPC地址:     %s\n", addr)
	fmt.Println("========================================")
	fmt.Println()

	fmt.Println("支持的业务操作:")
	fmt.Printf("  echo       - 回显客户端发送的数据\n")
	fmt.Printf("  time       - 返回当前服务器时间\n")
	fmt.Printf("  hello      - 返回问候消息 (参数: name)\n")
	fmt.Printf("  calculate  - 数学计算 (参数: operation, a, b)\n")
	fmt.Printf("             支持: add, subtract, multiply, divide\n")
	fmt.Printf("  status     - 返回服务器状态信息\n")
	fmt.Println()

	fmt.Println("gRPC服务接口:")
	fmt.Printf("  ProcessRequest - 处理业务请求\n")
	fmt.Printf("  GetStatus      - 获取服务状态\n")
	fmt.Println()
}

func getBuildTime() string {

	return "unknown"
}

func startHealthServer(grpcAddr string) {
	// 从gRPC地址解析端口，健康检查端口 = gRPC端口 + 1000
	grpcPort := extractPort(grpcAddr)
	healthPort := grpcPort + 1000
	httpAddr := fmt.Sprintf(":%d", healthPort)

	// 创建HTTP路由
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)

	log.Printf("启动HTTP健康检查服务器: %s (gRPC端口: %d)", httpAddr, grpcPort)

	// 启动HTTP服务器
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
	// 处理 ":8081" 格式
	if strings.HasPrefix(addr, ":") {
		if port, err := strconv.Atoi(addr[1:]); err == nil {
			return port
		}
	}

	// 处理 "host:port" 格式
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		if port, err := strconv.Atoi(addr[idx+1:]); err == nil {
			return port
		}
	}

	// 默认端口
	return 8081
}
