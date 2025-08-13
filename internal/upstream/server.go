// Package upstream 提供上游gRPC服务实现
package upstream

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"

	pb "gatesvr/proto"
)

type Server struct {
	pb.UnimplementedUpstreamServiceServer

	// 服务器配置
	addr string

	// 单播推送客户端（新增）
	unicastClient *UnicastClient

	// 服务状态
	startTime time.Time

	// 连接统计
	activeConnections int32
	connMutex         sync.RWMutex

	// 用户会话管理
	loggedInUsers map[string]*UserSession // key是openid
	usersMutex    sync.RWMutex

	// gRPC服务器
	grpcServer *grpc.Server
	listener   net.Listener

	// 停止信号
	stopCh chan struct{}
	wg     sync.WaitGroup
}

type UserSession struct {
	OpenID     string    `json:"openid"`
	SessionID  string    `json:"session_id"`
	LoginTime  time.Time `json:"login_time"`
	LastActive time.Time `json:"last_active"`
}

func NewServer(addr string) *Server {
	// 从环境变量读取网关gRPC地址，默认为localhost:8092
	gatewayAddr := os.Getenv("GATEWAY_GRPC_ADDR")
	if gatewayAddr == "" {
		gatewayAddr = "localhost:8092"
	}
	
	return &Server{
		addr:          addr,
		startTime:     time.Now(),
		unicastClient: NewUnicastClient(gatewayAddr), // 网关gRPC地址
		loggedInUsers: make(map[string]*UserSession),
		stopCh:        make(chan struct{}),
	}
}

func (s *Server) Start() error {
	log.Printf("正在启动上游服务器: %s", s.addr)

	// 连接到网关的单播推送服务
	if err := s.unicastClient.Connect(); err != nil {
		log.Printf("警告: 连接网关单播服务失败: %v", err)
	}

	// 监听端口
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("监听端口失败: %w", err)
	}
	s.listener = listener

	// 创建gRPC服务器
	s.grpcServer = grpc.NewServer(
		grpc.UnaryInterceptor(s.unaryInterceptor),
		grpc.StreamInterceptor(s.streamInterceptor),
	)

	// 注册服务
	pb.RegisterUpstreamServiceServer(s.grpcServer, s)

	// 启动定时广播任务
	s.wg.Add(1)
	go s.broadcastRoutine()

	log.Printf("上游服务器已启动: %s", s.addr)
	log.Printf("gRPC服务接口:")
	log.Printf("  ProcessRequest - 处理业务请求")
	log.Printf("  GetStatus      - 获取服务状态")

	// 启动gRPC服务
	return s.grpcServer.Serve(listener)
}

// Stop 停止服务器
func (s *Server) Stop() {
	log.Printf("正在停止上游服务器...")

	// 停止信号
	close(s.stopCh)

	// 关闭单播推送客户端
	if s.unicastClient != nil {
		s.unicastClient.Close()
	}

	// 停止gRPC服务器
	if s.grpcServer != nil {
		s.grpcServer.GracefulStop()
	}

	// 关闭监听器
	if s.listener != nil {
		s.listener.Close()
	}

	// 等待所有goroutine结束
	s.wg.Wait()

	log.Printf("上游服务器已停止")
}

func (s *Server) ProcessRequest(ctx context.Context, req *pb.UpstreamRequest) (*pb.UpstreamResponse, error) {
	log.Printf("收到业务请求 - 会话: %s, 动作: %s", req.SessionId, req.Action)

	// 根据动作类型处理不同的业务逻辑
	switch req.Action {
	case "login", "auth", "signin", "hello":
		return s.handleLogin(ctx, req)
	case "logout":
		return s.handleLogout(ctx, req)
	case "echo":
		return s.handleEcho(ctx, req)
	case "time":
		return s.handleTime(ctx, req)
	case "calculate":
		return s.handleCalculate(ctx, req)
	case "status":
		return s.handleStatus(ctx, req)
	case "user_list":
		return s.handleUserList(ctx, req)
	case "broadcast":
		return s.handleBroadcastCommand(ctx, req)
	case "before":
		return s.handleBeforeCommand(ctx, req)
	case "after":
		return s.handleAfterCommand(ctx, req)
	default:
		return s.handleDefault(ctx, req)
	}
}

func (s *Server) GetStatus(ctx context.Context, req *pb.StatusRequest) (*pb.StatusResponse, error) {
	s.connMutex.RLock()
	connCount := s.activeConnections
	s.connMutex.RUnlock()

	uptime := time.Since(s.startTime).Seconds()

	metadata := map[string]string{
		"version":    "1.0.0",
		"go_version": "1.21",
		"build_time": s.startTime.Format(time.RFC3339),
	}

	response := &pb.StatusResponse{
		Status:            "healthy",
		Uptime:            int64(uptime),
		ActiveConnections: connCount,
		Metadata:          metadata,
	}

	log.Printf("返回服务状态 - 运行时间: %.0f秒, 活跃连接: %d",
		uptime, connCount)

	return response, nil
}

func (s *Server) handleEcho(ctx context.Context, req *pb.UpstreamRequest) (*pb.UpstreamResponse, error) {
	message := string(req.Data)

	// 如果包含特殊参数，触发单播推送演示
	if gid := req.Params["demo_unicast_gid"]; gid != "" && s.unicastClient != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			err := s.unicastClient.PushToClient(ctx, "gid", gid, "echo", "Echo推送",
				fmt.Sprintf("您的echo消息: %s", message), req.Data)
			if err != nil {
				log.Printf("Echo单播推送失败: %v", err)
			}
		}()
	}

	return &pb.UpstreamResponse{
		Code:    200,
		Message: "success",
		Data:    []byte(fmt.Sprintf("Echo: %s", message)),
	}, nil
}

func (s *Server) handleTime(ctx context.Context, req *pb.UpstreamRequest) (*pb.UpstreamResponse, error) {
	now := time.Now()
	timeStr := now.Format("2006-01-02 15:04:05")

	return &pb.UpstreamResponse{
		Code:    200,
		Message: "当前时间",
		Data:    []byte(timeStr),
		Headers: map[string]string{
			"content-type": "text/plain",
			"timezone":     now.Location().String(),
			"unix":         fmt.Sprintf("%d", now.Unix()),
		},
	}, nil
}

func (s *Server) handleLogin(ctx context.Context, req *pb.UpstreamRequest) (*pb.UpstreamResponse, error) {
	if req.Openid == "" {
		return &pb.UpstreamResponse{
			Code:    400,
			Message: "OpenID不能为空",
			Data:    []byte("登录失败：缺少OpenID"),
		}, nil
	}

	// 记录用户登录
	s.usersMutex.Lock()
	s.loggedInUsers[req.Openid] = &UserSession{
		OpenID:     req.Openid,
		SessionID:  req.SessionId,
		LoginTime:  time.Now(),
		LastActive: time.Now(),
	}
	userCount := len(s.loggedInUsers)
	s.usersMutex.Unlock()

	name := req.Params["name"]
	if name == "" {
		name = req.Openid
	}

	message := fmt.Sprintf("你好, %s! 欢迎登录网关服务器。当前在线用户：%d人", name, userCount)
	log.Printf("用户登录成功 - OpenID: %s, SessionID: %s, 在线用户数: %d", req.Openid, req.SessionId, userCount)

	return &pb.UpstreamResponse{
		Code:    200,
		Message: "登录成功",
		Data:    []byte(message),
		Headers: map[string]string{
			"content-type": "text/plain",
			"language":     "zh-CN",
			"gid":          req.Openid, // 使用OpenID作为GID
			"zone":         "1",        // 示例Zone
		},
	}, nil
}

func (s *Server) handleLogout(ctx context.Context, req *pb.UpstreamRequest) (*pb.UpstreamResponse, error) {
	if req.Openid == "" {
		return &pb.UpstreamResponse{
			Code:    400,
			Message: "OpenID不能为空",
			Data:    []byte("登出失败：缺少OpenID"),
		}, nil
	}

	// 移除用户登录记录
	s.usersMutex.Lock()
	delete(s.loggedInUsers, req.Openid)
	userCount := len(s.loggedInUsers)
	s.usersMutex.Unlock()

	message := fmt.Sprintf("再见! 您已成功登出。当前在线用户：%d人", userCount)
	log.Printf("用户登出成功 - OpenID: %s, SessionID: %s, 剩余在线用户数: %d", req.Openid, req.SessionId, userCount)

	return &pb.UpstreamResponse{
		Code:    200,
		Message: "登出成功",
		Data:    []byte(message),
		Headers: map[string]string{
			"content-type": "text/plain",
		},
	}, nil
}

func (s *Server) handleUserList(ctx context.Context, req *pb.UpstreamRequest) (*pb.UpstreamResponse, error) {
	s.usersMutex.RLock()
	userCount := len(s.loggedInUsers)

	var userList []string
	for openid, session := range s.loggedInUsers {
		userInfo := fmt.Sprintf("OpenID: %s, 登录时间: %s",
			openid, session.LoginTime.Format("2006-01-02 15:04:05"))
		userList = append(userList, userInfo)
	}
	s.usersMutex.RUnlock()

	message := fmt.Sprintf("当前在线用户数: %d\n", userCount)
	if len(userList) > 0 {
		message += "用户列表:\n"
		for _, userInfo := range userList {
			message += userInfo + "\n"
		}
	}

	return &pb.UpstreamResponse{
		Code:    200,
		Message: "查询成功",
		Data:    []byte(message),
		Headers: map[string]string{
			"content-type": "text/plain",
			"user_count":   fmt.Sprintf("%d", userCount),
		},
	}, nil
}

func (s *Server) handleCalculate(ctx context.Context, req *pb.UpstreamRequest) (*pb.UpstreamResponse, error) {
	operation := req.Params["operation"]
	aStr := req.Params["a"]
	bStr := req.Params["b"]

	if operation == "" || aStr == "" || bStr == "" {
		return &pb.UpstreamResponse{
			Code:    400,
			Message: "缺少必需参数: operation, a, b",
			Data:    []byte("错误: 参数不完整"),
		}, nil
	}

	// 简单的整数计算示例
	var a, b int
	if _, err := fmt.Sscanf(aStr, "%d", &a); err != nil {
		return &pb.UpstreamResponse{
			Code:    400,
			Message: "参数a不是有效整数",
			Data:    []byte("错误: 参数a格式错误"),
		}, nil
	}

	if _, err := fmt.Sscanf(bStr, "%d", &b); err != nil {
		return &pb.UpstreamResponse{
			Code:    400,
			Message: "参数b不是有效整数",
			Data:    []byte("错误: 参数b格式错误"),
		}, nil
	}

	var result int
	var opStr string

	switch operation {
	case "add":
		result = a + b
		opStr = "+"
	case "subtract":
		result = a - b
		opStr = "-"
	case "multiply":
		result = a * b
		opStr = "*"
	case "divide":
		if b == 0 {
			return &pb.UpstreamResponse{
				Code:    400,
				Message: "除数不能为零",
				Data:    []byte("错误: 除零错误"),
			}, nil
		}
		result = a / b
		opStr = "/"
	default:
		return &pb.UpstreamResponse{
			Code:    400,
			Message: "不支持的运算操作",
			Data:    []byte("错误: 操作类型无效"),
		}, nil
	}

	message := fmt.Sprintf("%d %s %d = %d", a, opStr, b, result)

	return &pb.UpstreamResponse{
		Code:    200,
		Message: "计算成功",
		Data:    []byte(message),
		Headers: map[string]string{
			"content-type": "text/plain",
			"operation":    operation,
			"result":       fmt.Sprintf("%d", result),
		},
	}, nil
}

func (s *Server) handleStatus(ctx context.Context, req *pb.UpstreamRequest) (*pb.UpstreamResponse, error) {
	s.connMutex.RLock()
	connCount := s.activeConnections
	s.connMutex.RUnlock()

	uptime := time.Since(s.startTime)

	status := map[string]interface{}{
		"service":            "upstream-server",
		"status":             "healthy",
		"uptime_seconds":     int64(uptime.Seconds()),
		"uptime_human":       uptime.String(),
		"active_connections": connCount,
		"start_time":         s.startTime.Format(time.RFC3339),
		"current_time":       time.Now().Format(time.RFC3339),
	}

	statusJSON := ""
	for k, v := range status {
		statusJSON += fmt.Sprintf("%s: %v\n", k, v)
	}

	return &pb.UpstreamResponse{
		Code:    200,
		Message: "状态查询成功",
		Data:    []byte(statusJSON),
		Headers: map[string]string{
			"content-type": "text/plain",
			"uptime":       uptime.String(),
		},
	}, nil
}

func (s *Server) handleBroadcastCommand(ctx context.Context, req *pb.UpstreamRequest) (*pb.UpstreamResponse, error) {
	message := req.Params["message"]
	if message == "" {
		message = "系统广播消息"
	}

	// 立即发送一次广播
	userCount := s.sendBroadcastMessage(message, req.Data)

	responseMsg := fmt.Sprintf("广播消息已发送给 %d 个在线用户", userCount)

	return &pb.UpstreamResponse{
		Code:    200,
		Message: "广播成功",
		Data:    []byte(responseMsg),
		Headers: map[string]string{
			"content-type": "text/plain",
			"user_count":   fmt.Sprintf("%d", userCount),
		},
	}, nil
}

func (s *Server) handleBeforeCommand(ctx context.Context, req *pb.UpstreamRequest) (*pb.UpstreamResponse, error) {
	message := req.Params["message"]
	if message == "" {
		message = "测试notify在response之前"
	}

	// 发送notify消息（在response之前）
	if s.unicastClient != nil && req.Openid != "" {
		go func() {
			pushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			notifyContent := fmt.Sprintf("【NOTIFY BEFORE】%s - 时间: %s", message, time.Now().Format("15:04:05.000"))
			err := s.unicastClient.PushToOpenIDWithSyncHint(pushCtx, req.Openid, "before_test",
				"Notify Before Response", notifyContent, []byte("notify_before_data"),
				pb.NotifySyncHint_NSH_BEFORE_RESPONSE, req.ClientSeqId)
			if err != nil {
				log.Printf("Before notify推送失败: %v", err)
			} else {
				log.Printf("Before notify推送成功 - OpenID: %s, 消息: %s", req.Openid, notifyContent)
			}
		}()
	}

	// 模拟处理延迟
	time.Sleep(100 * time.Millisecond)

	responseMsg := fmt.Sprintf("【RESPONSE】%s - 处理完成时间: %s", message, time.Now().Format("15:04:05.000"))

	return &pb.UpstreamResponse{
		Code:        200,
		Message:     "before指令执行成功",
		Data:        []byte(responseMsg),
		ClientSeqId: req.ClientSeqId,
		Headers: map[string]string{
			"content-type": "text/plain",
			"test-type":    "before",
		},
	}, nil
}

func (s *Server) handleAfterCommand(ctx context.Context, req *pb.UpstreamRequest) (*pb.UpstreamResponse, error) {
	message := req.Params["message"]
	if message == "" {
		message = "测试notify在response之后"
	}

	// 发送notify消息（在response之后）
	if s.unicastClient != nil && req.Openid != "" {
		go func() {
			// 稍微延迟一下确保response先返回

			pushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			notifyContent := fmt.Sprintf("【NOTIFY AFTER】%s - 时间: %s", message, time.Now().Format("15:04:05.000"))
			err := s.unicastClient.PushToOpenIDWithSyncHint(pushCtx, req.Openid, "after_test",
				"Notify After Response", notifyContent, []byte("notify_after_data"),
				pb.NotifySyncHint_NSH_AFTER_RESPONSE, req.ClientSeqId)
			if err != nil {
				log.Printf("After notify推送失败: %v", err)
			} else {
				log.Printf("After notify推送成功 - OpenID: %s, 消息: %s", req.Openid, notifyContent)
			}
		}()
	}

	// 模拟处理延迟
	time.Sleep(100 * time.Millisecond)

	responseMsg := fmt.Sprintf("【RESPONSE】%s - 处理完成时间: %s", message, time.Now().Format("15:04:05.000"))

	return &pb.UpstreamResponse{
		Code:        200,
		Message:     "after指令执行成功",
		Data:        []byte(responseMsg),
		ClientSeqId: req.ClientSeqId,
		Headers: map[string]string{
			"content-type": "text/plain",
			"test-type":    "after",
		},
	}, nil
}

func (s *Server) handleDefault(ctx context.Context, req *pb.UpstreamRequest) (*pb.UpstreamResponse, error) {
	message := fmt.Sprintf("未知操作: %s\n可用操作: echo, time, hello, calculate, status", req.Action)

	return &pb.UpstreamResponse{
		Code:    404,
		Message: "操作不存在",
		Data:    []byte(message),
		Headers: map[string]string{
			"content-type": "text/plain",
		},
	}, nil
}

func (s *Server) unaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	// 增加活跃连接数
	s.connMutex.Lock()
	s.activeConnections++
	current := s.activeConnections
	s.connMutex.Unlock()

	log.Printf("处理gRPC请求: %s, 当前活跃连接: %d", info.FullMethod, current)

	// 处理请求
	start := time.Now()
	resp, err := handler(ctx, req)
	duration := time.Since(start)

	// 减少活跃连接数
	s.connMutex.Lock()
	s.activeConnections--
	current = s.activeConnections
	s.connMutex.Unlock()

	// 记录请求日志
	status := "成功"
	if err != nil {
		status = fmt.Sprintf("失败: %v", err)
	}

	log.Printf("gRPC请求完成: %s, 状态: %s, 耗时: %v, 剩余连接: %d",
		info.FullMethod, status, duration, current)

	return resp, err
}

func (s *Server) streamInterceptor(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	startTime := time.Now()

	// 增加活跃连接数
	s.connMutex.Lock()
	s.activeConnections++
	current := s.activeConnections
	s.connMutex.Unlock()

	log.Printf("处理gRPC流请求: %s, 当前活跃连接: %d", info.FullMethod, current)

	// 处理请求
	err := handler(srv, stream)

	// 减少活跃连接数
	s.connMutex.Lock()
	s.activeConnections--
	current = s.activeConnections
	s.connMutex.Unlock()

	// 记录请求日志
	status := "成功"
	if err != nil {
		status = fmt.Sprintf("失败: %v", err)
	}

	duration := time.Since(startTime)
	log.Printf("gRPC流请求完成: %s, 状态: %s, 耗时: %v, 剩余连接: %d",
		info.FullMethod, status, duration, current)

	return err
}

func (s *Server) broadcastRoutine() {
	defer s.wg.Done()

	ticker := time.NewTicker(30 * time.Second) // 每30秒广播一次
	defer ticker.Stop()

	broadcastCount := 0

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.usersMutex.RLock()
			userCount := len(s.loggedInUsers)
			s.usersMutex.RUnlock()

			// 只有在有在线用户时才广播
			if userCount > 0 {
				broadcastCount++
				message := fmt.Sprintf("定时广播消息 #%d - 当前时间: %s, 在线用户: %d人",
					broadcastCount,
					time.Now().Format("2006-01-02 15:04:05"),
					userCount)

				data := []byte(message)
				actualUserCount := s.sendBroadcastMessage(message, data)

				log.Printf("定时广播完成 #%d - 目标用户: %d, 实际发送: %d",
					broadcastCount, userCount, actualUserCount)
			}
		}
	}
}

func (s *Server) sendBroadcastMessage(message string, data []byte) int {
	if s.unicastClient == nil {
		log.Printf("广播失败：单播客户端未初始化")
		return 0
	}

	s.usersMutex.RLock()
	users := make([]*UserSession, 0, len(s.loggedInUsers))
	for _, user := range s.loggedInUsers {
		users = append(users, user)
	}
	s.usersMutex.RUnlock()

	if len(users) == 0 {
		log.Printf("没有在线用户，跳过广播")
		return 0
	}

	// 使用网关的广播功能
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := s.unicastClient.BroadcastToClients(ctx, "system", "系统广播", message, data, nil)
	if err != nil {
		log.Printf("广播发送失败: %v", err)
		return 0
	}

	log.Printf("广播消息已发送 - 目标用户数: %d, 消息: %s", len(users), message)
	return len(users)
}

func (s *Server) SendBroadcast(message string, data []byte, headers map[string]string) error {
	userCount := s.sendBroadcastMessage(message, data)
	if userCount > 0 {
		return nil
	}
	return fmt.Errorf("广播发送失败或没有在线用户")
}

func (s *Server) GetBroadcastStats() map[string]interface{} {
	s.usersMutex.RLock()
	userCount := len(s.loggedInUsers)
	var userList []string
	for openid, session := range s.loggedInUsers {
		userList = append(userList, fmt.Sprintf("%s(登录时间:%s)",
			openid, session.LoginTime.Format("15:04:05")))
	}
	s.usersMutex.RUnlock()

	return map[string]interface{}{
		"status":       "active",
		"online_users": userCount,
		"user_list":    userList,
		"uptime":       time.Since(s.startTime).String(),
	}
}

func (s *Server) GetLoggedInUsers() []*UserSession {
	s.usersMutex.RLock()
	defer s.usersMutex.RUnlock()

	users := make([]*UserSession, 0, len(s.loggedInUsers))
	for _, user := range s.loggedInUsers {
		users = append(users, &UserSession{
			OpenID:     user.OpenID,
			SessionID:  user.SessionID,
			LoginTime:  user.LoginTime,
			LastActive: user.LastActive,
		})
	}
	return users
}

func (s *Server) UpdateUserActivity(openid string) {
	s.usersMutex.Lock()
	defer s.usersMutex.Unlock()

	if user, exists := s.loggedInUsers[openid]; exists {
		user.LastActive = time.Now()
	}
}
