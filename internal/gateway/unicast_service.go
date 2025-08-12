package gateway

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"gatesvr/internal/session"
	pb "gatesvr/proto"

	"google.golang.org/protobuf/proto"
)

// 实现单播推送到指定客户端
func (s *Server) PushToClient(ctx context.Context, req *pb.UnicastPushRequest) (*pb.UnicastPushResponse, error) {
	log.Printf("收到单播推送请求 - 目标类型: %s, 目标ID: %s, 消息类型: %s, 同步提示: %v",
		req.TargetType, req.TargetId, req.MsgType, req.SyncHint)

	var err error
	switch req.TargetType {
	case "session":
		err = s.handleUnicastToSessionWithOrdering(req)
	case "openid":
		err = s.handleUnicastToOpenIDWithOrdering(req)
	default:
		err = fmt.Errorf("不支持的推送目标类型: %s", req.TargetType)
	}

	success := err == nil
	message := "推送成功"
	errorCode := ""

	if err != nil {
		message = err.Error()
		errorCode = "PUSH_FAILED"
	}

	return &pb.UnicastPushResponse{
		Success:   success,
		Message:   message,
		ErrorCode: errorCode,
	}, nil
}

// 会话的单播推送
func (s *Server) handleUnicastToSessionWithOrdering(req *pb.UnicastPushRequest) error {
	session, exists := s.sessionManager.GetSession(req.TargetId)
	if !exists {
		return fmt.Errorf("会话不存在: %s", req.TargetId)
	}

	// 检查会话状态
	if !session.IsNormal() {
		return errors.New("session not normal")
	}

	return s.processNotifyMessage(session, req)
}

func (s *Server) handleUnicastToOpenIDWithOrdering(req *pb.UnicastPushRequest) error {
	session, exists := s.sessionManager.GetSessionByOpenID(req.TargetId)
	if !exists {
		// OpenID对应的会话不存在，可能用户未连接，缓存消息
		return errors.New("session not exit")
	}

	// 检查会话状态
	if !session.IsNormal() {
		// 会话未激活，缓存消息
		return errors.New("session not normal")
	}

	// 会话已激活，根据SyncHint处理消息
	return s.processNotifyMessage(session, req)
}

func (s *Server) BroadcastToClients(ctx context.Context, req *pb.BroadcastRequest) (*pb.BroadcastResponse, error) {
	log.Printf("收到广播推送请求 - 消息类型: %s, 标题: %s", req.MsgType, req.Title)

	// 获取所有活跃会话
	sessions := s.sessionManager.GetAllSessions()
	successCount := int32(0)
	totalCount := int32(len(sessions))

	for _, session := range sessions {
		// 只向已激活的会话广播
		if !session.IsNormal() || session.IsClosed() {
			continue
		}

		// 发送广播消息
		if err := s.sendBroadcastMessage(session, req); err != nil {
			log.Printf("广播消息发送失败 - 会话: %s, 错误: %v", session.ID, err)
			continue
		}

		successCount++
	}

	message := fmt.Sprintf("广播消息已发送到 %d/%d 个在线客户端", successCount, totalCount)
	log.Printf("广播推送完成 - %s", message)

	return &pb.BroadcastResponse{
		SentCount: successCount,
		Message:   message,
	}, nil
}

func (s *Server) sendBroadcastMessage(session *session.Session, req *pb.BroadcastRequest) error {
	if session.IsClosed() {
		return fmt.Errorf("会话已关闭: %s", session.ID)
	}

	businessResp := &pb.BusinessResponse{
		Code:    200,
		Message: fmt.Sprintf("[%s] %s", req.MsgType, req.Title),
		Data:    req.Data,
	}

	payload, err := proto.Marshal(businessResp)
	if err != nil {
		return fmt.Errorf("序列化广播BusinessResponse失败: %v", err)
	}

	// 直接使用有序发送器发送广播消息
	if err := s.orderedSender.PushBusinessData(session, payload); err != nil {
		return fmt.Errorf("发送广播消息失败: %w", err)
	}

	return nil
}

func (s *Server) processNotifyMessage(session *session.Session, req *pb.UnicastPushRequest) error {
	switch req.SyncHint {
	case pb.NotifySyncHint_NSH_BEFORE_RESPONSE:
		// 绑定到指定response之前发送
		grid := uint32(req.BindClientSeqId)
		if grid == 0 {
			return fmt.Errorf("bind_client_seq_id is required for NSH_BEFORE_RESPONSE")
		}
		notifyItem := s.createNotifyBindMsgItem(req)
		if !session.AddNotifyBindBeforeRsp(grid, notifyItem) {
			return fmt.Errorf("failed to bind notify before response for grid: %d", grid)
		}

		log.Printf("Notify消息已绑定到response之前 - grid: %d, 会话: %s", grid, session.ID)
		return nil

	case pb.NotifySyncHint_NSH_AFTER_RESPONSE:
		// 绑定到指定response之后发送
		grid := uint32(req.BindClientSeqId)
		if grid == 0 {
			return fmt.Errorf("bind_client_seq_id is required for NSH_AFTER_RESPONSE")
		}
		notifyItem := s.createNotifyBindMsgItem(req)
		if !session.AddNotifyBindAfterRsp(grid, notifyItem) {
			return fmt.Errorf("failed to bind notify after response for grid: %d", grid)
		}

		log.Printf("Notify消息已绑定到response之后 - grid: %d, 会话: %s", grid, session.ID)
		return nil

	case pb.NotifySyncHint_NSH_IMMEDIATELY:
		fallthrough
	default:
		// 立即发送
		businessResp := &pb.BusinessResponse{
			Code:    200,
			Message: fmt.Sprintf("[%s] %s", req.MsgType, req.Title),
			Data:    req.Data,
		}
		payload, err := proto.Marshal(businessResp)
		if err != nil {
			return fmt.Errorf("序列化立即notify BusinessResponse失败: %w", err)
		}

		if err := s.orderedSender.PushBusinessData(session, payload); err != nil {
			return fmt.Errorf("发送立即notify失败: %w", err)
		}

		log.Printf("立即notify消息发送成功 - 会话: %s, 类型: %s, 标题: %s", session.ID, req.MsgType, req.Title)
		return nil
	}
}

// 创建NotifyBindMsgItem
func (s *Server) createNotifyBindMsgItem(req *pb.UnicastPushRequest) *session.NotifyBindMsgItem {
	notifyData, err := proto.Marshal(req)
	if err != nil {
		log.Printf("序列化notify消息失败: %v", err)
		notifyData = req.Data
	}

	return &session.NotifyBindMsgItem{
		NotifyData: notifyData,
		MsgType:    req.MsgType,
		Title:      req.Title,
		Content:    req.Content,
		Metadata:   req.Metadata,
		SyncHint:   req.SyncHint,
		BindGrid:   uint32(req.BindClientSeqId),
		CreateTime: time.Now().Unix(),
	}
}

// 确保Server实现了GatewayService接口
var _ pb.GatewayServiceServer = (*Server)(nil)
