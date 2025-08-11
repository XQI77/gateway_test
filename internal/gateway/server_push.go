package gateway

import (
	"fmt"
	"log"
)

// 单播推送消息到指定会话
func (s *Server) PushToSession(sessionID string, msgType string, title, content string, data []byte) error {
	session, exists := s.sessionManager.GetSession(sessionID)
	if !exists {
		return fmt.Errorf("会话不存在: %s", sessionID)
	}

	if session.IsClosed() {
		return fmt.Errorf("会话已关闭: %s", sessionID)
	}

	if err := s.orderedSender.PushBusinessData(session, data); err != nil {
		log.Printf("发送推送消息失败: %v", err)
		return fmt.Errorf("发送推送消息失败: %w", err)
	}

	log.Printf("单播推送成功 - 会话: %s, 类型: %s, 标题: %s", sessionID, msgType, title)
	return nil
}

// 根据OpenID单播推送消息
func (s *Server) PushToOpenID(openID string, msgType string, title, content string, data []byte) error {
	session, exists := s.sessionManager.GetSessionByOpenID(openID)
	if !exists {
		return fmt.Errorf("OpenID对应的会话不存在: %s", openID)
	}

	return s.PushToSession(session.ID, msgType, title, content, data)
}

// 处理上游服务的单播推送请求
func (s *Server) HandleUnicastPushRequest(targetType string, targetID string, msgType string, title, content string, data []byte) error {
	switch targetType {
	case "session":
		return s.PushToSession(targetID, msgType, title, content, data)
	case "openid":
		return s.PushToOpenID(targetID, msgType, title, content, data)
	default:
		return fmt.Errorf("不支持的推送目标类型: %s", targetType)
	}
}
