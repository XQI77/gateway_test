// Package gateway 提供消息处理功能
package gateway

import (
	"context"
	"fmt"
	"gatesvr/internal/session"
	pb "gatesvr/proto"
	"google.golang.org/protobuf/proto"
	"log"
	"strconv"
	"strings"
)

// 处理心跳消息
func (s *Server) handleHeartbeat(sess *session.Session, req *pb.ClientRequest) bool {

	if req.SeqId != 0 {
		log.Printf("心跳消息序列号应为0 - 会话: %s, 实际序列号: %d", sess.ID, req.SeqId)
		return false
	}

	heartbeatReq := &pb.HeartbeatRequest{}
	if err := proto.Unmarshal(req.Payload, heartbeatReq); err != nil {
		log.Printf("解析心跳请求失败: %v", err)
		return false
	}

	// 发送心跳响应
	if err := s.orderedSender.SendHeartbeatResponse(sess, req.MsgId, heartbeatReq.ClientTimestamp); err != nil {
		log.Printf("发送心跳响应失败: %v", err)
		return false
	}

	log.Printf("处理心跳请求 - 客户端: %s, 会话: %s", sess.ClientID, sess.ID)
	return true
}

// 处理业务请求
func (s *Server) handleBusinessRequest(ctx context.Context, sess *session.Session, req *pb.ClientRequest) bool {
	log.Printf("处理业务请求 - 动作: %s", sess.ID)

	if !s.sessionManager.ValidateClientSequence(sess.ID, req.SeqId) {
		expectedSeq := s.sessionManager.GetExpectedClientSequence(sess.ID)
		log.Printf("业务消息序列号验证失败 - 会话: %s, 实际序列号: %d, 期待序列号: %d",
			sess.ID, req.SeqId, expectedSeq)
		s.sendErrorResponse(sess, req.MsgId, 400, "无效的序列号",
			fmt.Sprintf("序列号必须递增且连续，期待: %d, 实际: %d", expectedSeq, req.SeqId))
		return false
	}

	// 解析业务请求
	businessReq := &pb.BusinessRequest{}
	if err := proto.Unmarshal(req.Payload, businessReq); err != nil {
		log.Printf("解析业务请求失败: %v", err)
		s.sendErrorResponse(sess, req.MsgId, 400, "解析请求失败", err.Error())
		return false
	}

	isLoginAction := strings.ToLower(businessReq.Action) == "hello"

	if isLoginAction {
		// 登录请求：必须在Inited或Normal状态
		if !sess.CanProcessLoginRequest() {
			log.Printf("会话状态不允许登录请求 - 会话: %s, 状态: %d", sess.ID, sess.State())
			s.sendErrorResponse(sess, req.MsgId, 403, "会话状态错误", "当前状态不允许登录")
			return false
		}
	} else {
		if !sess.CanProcessBusinessRequest() {
			log.Printf("会话未登录，拒绝业务请求 - 会话: %s, 动作: %s, 状态: %d", sess.ID, businessReq.Action, sess.State())
			s.sendErrorResponse(sess, req.MsgId, 401, "未登录", "请先登录")
			return false
		}
	}
	if !s.overloadProtector.AllowUpstreamRequest() {
		log.Printf("上游请求被过载保护拒绝 - 会话: %s, 动作: %s", sess.ID, businessReq.Action)
		s.sendErrorResponse(sess, req.MsgId, 503, "上游服务繁忙", "上游服务当前负载过高，请稍后重试")
		return false
	}

	s.overloadProtector.OnUpstreamStart()
	defer s.overloadProtector.OnUpstreamEnd() // 确保在函数结束时调用

	upstreamReq := &pb.UpstreamRequest{
		SessionId:   sess.ID,
		Openid:      sess.OpenID,
		Action:      businessReq.Action,
		Params:      businessReq.Params,
		Data:        businessReq.Data,
		Headers:     req.Headers,
		ClientSeqId: req.SeqId,
	}

	// 创建带超时的上游请求上下文
	upstreamCtx := ctx
	if s.overloadProtector.config != nil && s.overloadProtector.config.UpstreamTimeout > 0 {
		var cancel context.CancelFunc
		upstreamCtx, cancel = context.WithTimeout(ctx, s.overloadProtector.config.UpstreamTimeout)
		defer cancel()
	}

	serviceInfo := s.getUpstreamServiceInfo(sess.OpenID)

	log.Printf("路由业务请求 - 动作: %s, OpenID: %s, 目标服务: %s", businessReq.Action, sess.OpenID, serviceInfo)

	// Hello请求必须同步处理，等待上游服务返回结果
	if isLoginAction {
		log.Printf("Hello请求使用同步处理 - 会话: %s, 动作: %s", sess.ID, businessReq.Action)
		return s.handleBusinessRequestSync(ctx, sess, req, businessReq, upstreamReq,
			upstreamCtx, isLoginAction)
	}

	taskID := fmt.Sprintf("%s-%d", sess.ID, req.SeqId)

	// 为异步任务创建独立的上下文，避免与请求处理生命周期绑定
	asyncCtx := context.Background()
	if s.overloadProtector.config != nil && s.overloadProtector.config.UpstreamTimeout > 0 {
		var cancel context.CancelFunc
		asyncCtx, cancel = context.WithTimeout(context.Background(), s.overloadProtector.config.UpstreamTimeout)
		_ = cancel
	}

	asyncTask := &AsyncTask{
		TaskID:      taskID,
		SessionID:   sess.ID,
		Session:     sess,
		Request:     req,
		BusinessReq: businessReq,
		UpstreamReq: upstreamReq,
		Context:     asyncCtx,
		IsLogin:     isLoginAction,
	}

	if s.asyncProcessor == nil {
		log.Printf("异步处理器未启用，使用同步处理 - 会话: %s", sess.ID)
		return s.handleBusinessRequestSync(ctx, sess, req, businessReq, upstreamReq,
			upstreamCtx, isLoginAction)
	}

	if !s.asyncProcessor.SubmitTask(asyncTask) {
		log.Printf("异步队列已满，拒绝请求 - 会话: %s, 动作: %s", sess.ID, businessReq.Action)
		s.sendErrorResponse(sess, req.MsgId, 503, "服务繁忙", "异步处理队列已满，请稍后重试")
		return false
	}

	log.Printf("业务请求已提交异步处理 - 动作: %s, 会话: %s, 任务ID: %s",
		businessReq.Action, sess.ID, taskID)
	return true
}

// 同步处理业务请求
func (s *Server) handleBusinessRequestSync(ctx context.Context, sess *session.Session, req *pb.ClientRequest,
	businessReq *pb.BusinessRequest, upstreamReq *pb.UpstreamRequest,
	upstreamCtx context.Context, isLoginAction bool) bool {

	upstreamResp, err := s.callUpstreamService(upstreamCtx, sess.OpenID, upstreamReq)

	if err != nil {
		serviceInfo := s.getUpstreamServiceInfo(sess.OpenID)
		log.Printf("调用上游服务失败 - 服务: %s, 错误: %v", serviceInfo, err)
		s.metrics.IncError("upstream_error")
		s.sendErrorResponse(sess, req.MsgId, 500, "上游服务错误", err.Error())
		return false
	}

	if isLoginAction && upstreamResp.Code == 200 {
		if err := s.handleLoginSuccess(sess, upstreamResp); err != nil {
			log.Printf("处理登录失败: %v", err)
			s.sendErrorResponse(sess, req.MsgId, 500, "登录处理失败", err.Error())
			return false
		}
	}
	if err := s.orderedSender.SendBusinessResponse(sess, req.MsgId, upstreamResp.Code, upstreamResp.Message, upstreamResp.Data, upstreamResp.Headers); err != nil {
		log.Printf("发送业务响应失败: %v", err)
		return false
	}

	grid := uint32(req.SeqId)
	if err := s.processBoundNotifies(sess, grid); err != nil {
		log.Printf("处理绑定notify消息失败: %v", err)
	}

	log.Printf("同步处理业务请求完成 - 动作: %s, 会话: %s, 响应码: %d",
		businessReq.Action, sess.ID, upstreamResp.Code,
	)
	return true
}

// 处理登录成功后的会话激活
func (s *Server) handleLoginSuccess(sess *session.Session, upstreamResp *pb.UpstreamResponse) error {
	var zone int64
	var err error

	if zoneStr, exists := upstreamResp.Headers["zone"]; exists && zoneStr != "" {
		zone, err = strconv.ParseInt(zoneStr, 10, 64)
		if err != nil {
			return fmt.Errorf("解析Zone失败: %v", err)
		}
	}
	if err := s.sessionManager.ActivateSession(sess.ID, zone); err != nil {
		return fmt.Errorf("激活会话失败: %v", err)
	}

	log.Printf("登录成功，会话已激活 - 会话: %s, Zone: %d", sess.ID, zone)
	return nil
}

// 处理ACK消息
func (s *Server) handleAck(sess *session.Session, req *pb.ClientRequest) bool {

	if req.SeqId != 0 {
		log.Printf("ACK消息序列号应为0 - 会话: %s, 实际序列号: %d", sess.ID, req.SeqId)
		return false
	}

	ack := &pb.ClientAck{}
	if err := proto.Unmarshal(req.Payload, ack); err != nil {
		log.Printf("解析ACK消息失败: %v", err)
		return false
	}

	ackedCount := s.sessionManager.AckMessagesUpTo(sess.ID, ack.AckedSeqId)
	s.orderedSender.ResyncSessionSequence(sess, ack.AckedSeqId)
	s.metrics.SetOutboundQueueSize(sess.ID, s.sessionManager.GetPendingCount(sess.ID))

	if ackedCount > 0 {
		log.Printf("收到ACK确认 - 序列号: %d, 会话: %s, 清除消息数: %d", ack.AckedSeqId, sess.ID, ackedCount)
		return true
	} else {
		log.Printf("ACK确认无效 - 序列号: %d, 会话: %s", ack.AckedSeqId, sess.ID)
		return false
	}
}

// 发送错误响应
func (s *Server) sendErrorResponse(sess *session.Session, msgID uint32, code int32, message, detail string) {
	if err := s.orderedSender.SendErrorResponse(sess, msgID, code, message, detail); err != nil {
		log.Printf("发送错误响应失败: %v", err)
	}
}

// 同步处理START消息
func (s *Server) handleStartSync(sess *session.Session, req *pb.ClientRequest) bool {
	log.Printf("同步处理start请求 - 会话: %s, 消息ID: %d", sess.ID, req.MsgId)

	if req.SeqId != 0 {
		log.Printf("START消息序列号应为0 - 会话: %s, 实际序列号: %d", sess.ID, req.SeqId)
		s.sendErrorResponse(sess, req.MsgId, 400, "无效的序列号", "START消息序列号必须为0")
		return false
	}

	startReq := &pb.StartRequest{}
	if err := proto.Unmarshal(req.Payload, startReq); err != nil {
		log.Printf("解析连接建立请求失败: %v", err)
		s.sendErrorResponse(sess, req.MsgId, 400, "解析请求失败", err.Error())
		return false
	}

	if startReq.Openid == "" {
		log.Printf("连接建立请求缺少OpenID - 会话: %s", sess.ID)
		s.sendErrorResponse(sess, req.MsgId, 400, "缺少OpenID", "OpenID不能为空")
		return false
	}

	sess.OpenID = startReq.Openid
	sess.ClientID = req.Openid

	if err := s.sessionManager.BindSession(sess); err != nil {
		log.Printf("绑定session到openid失败: %v", err)
		s.sendErrorResponse(sess, req.MsgId, 500, "会话绑定失败", err.Error())
		return false
	}
	if startReq.LastAckedSeqId > 0 {
		ackedCount := s.sessionManager.AckMessagesUpTo(sess.ID, startReq.LastAckedSeqId)

		s.orderedSender.ResyncSessionSequence(sess, startReq.LastAckedSeqId)

		log.Printf("重连时清除已确认消息 - 会话: %s, 最后确认序列号: %d, 清除数量: %d",
			sess.ID, startReq.LastAckedSeqId, ackedCount)
	}

	connectionID := sess.ID
	if err := s.orderedSender.SendStartResponse(sess, req.MsgId, true, nil, 30, connectionID); err != nil {
		log.Printf("发送连接建立响应失败: %v", err)
		return false
	}

	log.Printf("同步处理连接建立请求成功 - OpenID: %s, 客户端: %s, 会话: %s, 状态: %d",
		startReq.Openid, sess.ClientID, sess.ID, sess.State())

	return true
}

// 处理连接断开请求
func (s *Server) handleStop(sess *session.Session, req *pb.ClientRequest) bool {

	stopReq := &pb.StopRequest{}
	if err := proto.Unmarshal(req.Payload, stopReq); err != nil {
		log.Printf("解析连接断开请求失败: %v", err)
		return false
	}

	reason := "client_requested"
	switch stopReq.Reason {
	case pb.StopRequest_USER_LOGOUT:
		reason = "user_logout"
	case pb.StopRequest_APP_CLOSE:
		reason = "app_close"
	case pb.StopRequest_SWITCH_ACCOUNT:
		reason = "switch_account"
	default:
		reason = "unknown"
	}

	log.Printf("收到连接断开请求 - 客户端: %s, 会话: %s, 原因: %s",
		sess.ClientID, sess.ID, reason)

	s.sessionManager.RemoveSessionWithDelay(sess.ID, false, reason)

	log.Printf("处理连接断开请求完成 - 会话: %s, 原因: %s", sess.ID, reason)

	return false
}

// 处理绑定在response前后的notify消息
func (s *Server) processBoundNotifies(sess *session.Session, grid uint32) error {
	if err := s.sendBeforeNotifies(sess, grid); err != nil {
		log.Printf("发送before-notify失败: %v", err)
		return err
	}

	if err := s.sendAfterNotifies(sess, grid); err != nil {
		log.Printf("发送after-notify失败: %v", err)
		return err
	}

	return nil
}

// 发送绑定在response之前的notify消息
func (s *Server) sendBeforeNotifies(sess *session.Session, grid uint32) error {
	if sess.GetOrderingManager() == nil {
		return nil
	}

	beforeNotifies := sess.GetOrderingManager().GetAndRemoveBeforeNotifies(grid)
	if len(beforeNotifies) == 0 {
		return nil
	}

	for _, notifyItem := range beforeNotifies {
		if err := s.orderedSender.PushBusinessData(sess, notifyItem.NotifyData); err != nil {
			log.Printf("发送before-notify失败 - 会话: %s, 错误: %v", sess.ID, err)
			return err
		}
		log.Printf("发送before-notify成功 - 会话: %s, 类型: %s", sess.ID, notifyItem.MsgType)
	}

	return nil
}

// 发送绑定在response之后的notify消息
func (s *Server) sendAfterNotifies(sess *session.Session, grid uint32) error {
	if sess.GetOrderingManager() == nil {
		return nil
	}

	afterNotifies := sess.GetOrderingManager().GetAndRemoveAfterNotifies(grid)
	if len(afterNotifies) == 0 {
		return nil
	}

	for _, notifyItem := range afterNotifies {
		if err := s.orderedSender.PushBusinessData(sess, notifyItem.NotifyData); err != nil {
			log.Printf("发送after-notify失败 - 会话: %s, 错误: %v", sess.ID, err)
			return err
		}
		log.Printf("发送after-notify成功 - 会话: %s, 类型: %s", sess.ID, notifyItem.MsgType)
	}

	return nil
}
