package gateway

import (
	"context"
	"io"
	"log"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"gatesvr/internal/session"
	pb "gatesvr/proto"

	"github.com/quic-go/quic-go"
)

// 异步消息读取事件驱动实现
type MessageEvent struct {
	Data      []byte
	Err       error
	ReadTime  time.Time // 读取完成时间
	StartTime time.Time // 开始读取时间
}

// 接受QUIC连接
func (s *Server) acceptConnections(ctx context.Context) {
	defer s.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		default:
			// 接受新连接
			conn, err := s.quicListener.Accept(ctx)
			if err != nil {
				if !s.isRunning() {
					return
				}
				log.Printf("接受QUIC连接失败: %v", err)
				s.performanceTracker.RecordError()
				continue
			}

			if !s.overloadProtector.AllowNewConnection() {
				log.Printf("连接被过载保护拒绝 - 来源: %s", conn.RemoteAddr())
				conn.CloseWithError(503, "server overloaded")
				continue
			}
			s.performanceTracker.RecordConnection()
			s.overloadProtector.OnConnectionStart()

			// 为每个连接启动处理goroutine
			s.wg.Add(1)
			go s.handleConnection(ctx, conn)
		}
	}
}

// 处理单个QUIC连接
func (s *Server) handleConnection(ctx context.Context, conn *quic.Conn) {
	defer s.wg.Done()
	defer func() {
		conn.CloseWithError(0, "connection closed")
		s.performanceTracker.RecordDisconnection()
		s.overloadProtector.OnConnectionEnd() // 连接结束时通知过载保护器
	}()

	log.Printf("新的QUIC连接: %s", conn.RemoteAddr())

	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		log.Printf("接受流失败: %v", err)
		s.performanceTracker.RecordError()
		return
	}
	defer stream.Close()

	// 处理第一个消息并创建会话
	session, firstMsgData, err := s.handleFirstMessage(ctx, conn, stream)
	if err != nil {
		return
	}

	log.Printf("创建会话: %s", session.ID)
	s.metrics.SetActiveConnections(s.sessionManager.GetSessionCount())

	// 处理第一个消息
	firstMsgEvent := MessageEvent{
		Data:      firstMsgData,
		Err:       nil,
		ReadTime:  time.Now(),
		StartTime: time.Now(),
	}
	s.handleMessageEvent(ctx, session, firstMsgEvent)

	// 处理后续消息
	s.handleSubsequentMessages(ctx, session, stream)
}

// 处理单个消息事件
func (s *Server) handleMessageEvent(ctx context.Context, session *session.Session, event MessageEvent) {
	log.Printf("处理消息 - 会话: %s, 读取时间: %v, 处理开始时间: %v", session.ID, event.ReadTime, event.StartTime)

	session.UpdateActivity()
	s.metrics.IncQPS()
	s.performanceTracker.RecordRequest()
	clientReq, err := s.messageCodec.DecodeClientRequest(event.Data)
	if err != nil {
		log.Printf("解码消息失败 - 会话: %s, 错误: %v", session.ID, err)
		s.metrics.IncError("decode_error")
		s.performanceTracker.RecordError()
		return
	}

	if clientReq.Type != pb.RequestType_REQUEST_HEARTBEAT &&
		clientReq.Type != pb.RequestType_REQUEST_ACK {
		if !s.overloadProtector.AllowNewRequest() {
			log.Printf("请求被过载保护拒绝 - 会话: %s, 类型: %d", session.ID, clientReq.Type)
			s.sendOverloadErrorResponse(session, clientReq)
			s.performanceTracker.RecordError()
			return
		}
	}

	// 处理不同类型的消息
	var success bool
	switch clientReq.Type {
	case pb.RequestType_REQUEST_START:
		success = s.handleStartSync(session, clientReq)
	case pb.RequestType_REQUEST_STOP:
		success = s.handleStop(session, clientReq)
	case pb.RequestType_REQUEST_HEARTBEAT:
		success = s.handleHeartbeat(session, clientReq)
	case pb.RequestType_REQUEST_BUSINESS:
		success = s.handleBusinessRequest(ctx, session, clientReq)
	case pb.RequestType_REQUEST_ACK:
		success = s.handleAck(session, clientReq)
	default:
		log.Printf("未知消息类型: %d - 会话: %s", clientReq.Type, session.ID)
		s.metrics.IncError("unknown_message_type")
		s.performanceTracker.RecordError()
		success = false
	}

	log.Printf("消息处理完成 - 会话: %s, 类型: %d, 成功: %t",
		session.ID, clientReq.Type, success)
}

// 处理第一个消息并创建会话
func (s *Server) handleFirstMessage(ctx context.Context, conn *quic.Conn, stream *quic.Stream) (*session.Session, []byte, error) {
	log.Printf("等待第一个消息以获取身份标识 - 连接: %s", conn.RemoteAddr())

	firstMsgData, err := s.messageCodec.ReadMessage(stream)
	if err != nil {
		log.Printf("读取第一个消息失败 - 连接: %s, 错误: %v", conn.RemoteAddr(), err)
		s.performanceTracker.RecordError()
		return nil, nil, err
	}

	firstClientReq, err := s.messageCodec.DecodeClientRequest(firstMsgData)
	if err != nil {
		log.Printf("解析第一个消息失败 - 连接: %s, 错误: %v", conn.RemoteAddr(), err)
		s.performanceTracker.RecordError()
		return nil, nil, err
	}

	var openID, clientID string
	if firstClientReq.Type == pb.RequestType_REQUEST_START {
		startReq := &pb.StartRequest{}
		if err := proto.Unmarshal(firstClientReq.Payload, startReq); err == nil {
			openID = startReq.Openid
			clientID = startReq.ClientId
		}
	} else if firstClientReq.Type == pb.RequestType_REQUEST_BUSINESS {
		businessReq := &pb.BusinessRequest{}
		if err := proto.Unmarshal(firstClientReq.Payload, businessReq); err == nil {
			if params := businessReq.Params; params != nil {
				if uid, exists := params["Openid"]; exists {
					openID = uid
				}
				if cid, exists := params["clientId"]; exists {
					clientID = cid
				}
			}
		}
	}

	userIP := conn.RemoteAddr().String()

	log.Printf("从第一个消息中提取身份信息 - OpenID: %s, ClientID: %s", openID, clientID)

	// 使用提取到的身份信息创建或重连会话
	session, isReconnect := s.sessionManager.CreateOrReconnectSession(conn, stream, clientID, openID, userIP)
	log.Printf("根据第一个消息提取出session - sessionid: %s", session.ID)
	if isReconnect {
		log.Printf("检测到重连 - 会话: %s, 客户端: %s, 用户: %s", session.ID, clientID, openID)
	}

	return session, firstMsgData, nil
}

// 处理后续消息
func (s *Server) handleSubsequentMessages(ctx context.Context, session *session.Session, stream *quic.Stream) {

	msgChan := make(chan MessageEvent, 32)
	readCtx, readCancel := context.WithCancel(ctx)
	defer readCancel()

	// 启动异步读取goroutine
	var readWg sync.WaitGroup
	readWg.Add(1)
	go func() {
		defer readWg.Done()
		defer close(msgChan)

		for {
			select {
			case <-readCtx.Done():
				log.Printf("读取协程收到取消信号 - 会话: %s", session.ID)
				return
			default:
				readStartTime := time.Now()
				data, err := s.messageCodec.ReadMessage(stream)
				readEndTime := time.Now()

				// 发送消息事件
				select {
				case msgChan <- MessageEvent{
					Data:      data,
					Err:       err,
					ReadTime:  readEndTime,
					StartTime: readStartTime,
				}:
				case <-readCtx.Done():
					return
				}
				if err != nil {
					log.Printf("读取消息出错，退出读取协程 - 会话: %s, 错误: %v", session.ID, err)
					return
				}
			}
		}
	}()

	log.Printf("开始事件驱动消息处理 - 会话: %s", session.ID)

	for {
		select {
		case <-ctx.Done():
			log.Printf("收到上下文取消信号 - 会话: %s", session.ID)
			readCancel()
			readWg.Wait()
			return

		case <-s.stopCh:
			log.Printf("收到服务器停止信号 - 会话: %s", session.ID)
			readCancel()
			readWg.Wait()
			return

		case event, ok := <-msgChan:
			if !ok {
				log.Printf("消息通道已关闭 - 会话: %s", session.ID)
				readWg.Wait()
				return
			}

			// 处理消息事件
			if event.Err != nil {
				if event.Err == io.EOF {
					log.Printf("客户端正常断开连接 - 会话: %s", session.ID)
				} else {
					log.Printf("读取消息失败 - 会话: %s, 错误: %v", session.ID, event.Err)
					s.metrics.IncError("read_error")
					s.performanceTracker.RecordError()
				}
				readCancel()
				readWg.Wait()
				return
			}

			s.handleMessageEvent(ctx, session, event)
		}
	}
}

// 发送过载错误响应
func (s *Server) sendOverloadErrorResponse(session *session.Session, req *pb.ClientRequest) {
	switch req.Type {
	case pb.RequestType_REQUEST_START:
		if err := s.orderedSender.SendStartResponse(session, req.MsgId, false,
			nil, 0, ""); err != nil {
			log.Printf("发送START过载错误响应失败: %v", err)
		}
	case pb.RequestType_REQUEST_BUSINESS:
		if err := s.orderedSender.SendErrorResponse(session, req.MsgId, 503, "服务器过载", "请求过多，请稍后重试"); err != nil {
			log.Printf("发送业务请求过载错误响应失败: %v", err)
		}
	case pb.RequestType_REQUEST_STOP:
	default:
		if err := s.orderedSender.SendErrorResponse(session, req.MsgId, 503, "服务器过载", "请求过多，请稍后重试"); err != nil {
			log.Printf("发送过载错误响应失败: %v", err)
		}
	}
}
