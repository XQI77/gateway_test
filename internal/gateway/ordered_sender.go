package gateway

import (
	"fmt"
	"log"

	"gatesvr/internal/message"
	"gatesvr/internal/session"
	pb "gatesvr/proto"
)

type OrderedMessageSender struct {
	server       *Server
	messageCodec *message.MessageCodec
}

// 创建有序消息发送器
func NewOrderedMessageSender(server *Server) *OrderedMessageSender {
	return &OrderedMessageSender{
		server:       server,
		messageCodec: server.messageCodec,
	}
}

func (oms *OrderedMessageSender) SendOrderedMessage(sess *session.Session, push *pb.ServerPush) error {
	serverSeq := sess.NewServerSeq()
	push.SeqId = serverSeq

	data, err := oms.messageCodec.EncodeServerPush(push)
	if err != nil {
		log.Printf("编码消息失败: %v", err)
		oms.server.metrics.IncError("encode_error")
		return fmt.Errorf("编码消息失败: %w", err)
	}

	orderedQueue := sess.GetOrderedQueue()
	if orderedQueue == nil {
		return fmt.Errorf("会话 %s 的有序队列未初始化", sess.ID)
	}

	if orderedQueue.GetSendCallback() == nil {
		orderedQueue.SetSendCallback(func(orderedMsg *session.OrderedMessage) error {
			return oms.sendMessageDirectly(sess, orderedMsg)
		})
		log.Printf("为会话设置发送回调函数 - 会话: %s", sess.ID)
	}
	if err := orderedQueue.EnqueueMessage(serverSeq, push, data); err != nil {
		log.Printf("消息加入有序队列失败: %v", err)
		return fmt.Errorf("消息加入有序队列失败: %w", err)
	}

	log.Printf("消息已加入有序队列 - 序列号: %d, 会话: %s, 类型: %d",
		serverSeq, sess.ID, push.Type)

	return nil
}

// 直接发送消息
func (oms *OrderedMessageSender) sendMessageDirectly(sess *session.Session, orderedMsg *session.OrderedMessage) error {
	if sess.IsClosed() {
		return fmt.Errorf("会话已关闭")
	}

	if err := oms.messageCodec.WriteMessage(sess.Stream, orderedMsg.Data); err != nil {
		log.Printf("发送消息失败: %v", err)
		oms.server.metrics.IncError("send_error")
		return fmt.Errorf("发送消息失败: %w", err)
	}

	oms.server.metrics.AddThroughput("outbound", int64(len(orderedMsg.Data)))

	log.Printf("有序消息发送成功 - 序列号: %d, 会话: %s",
		orderedMsg.ServerSeq, sess.ID)

	return nil
}

func (oms *OrderedMessageSender) SendHeartbeatResponse(sess *session.Session, msgID uint32, clientTimestamp int64) error {
	response := oms.messageCodec.CreateHeartbeatResponse(
		msgID,
		0,
		clientTimestamp,
	)

	return oms.SendOrderedMessage(sess, response)
}

func (oms *OrderedMessageSender) SendBusinessResponse(sess *session.Session, msgID uint32, code int32, message string, data []byte, headers map[string]string) error {
	response := oms.messageCodec.CreateBusinessResponse(
		msgID,
		0,
		code,
		message,
		data,
	)
	response.Headers = headers

	return oms.SendOrderedMessage(sess, response)
}

// 发送错误响应
func (oms *OrderedMessageSender) SendErrorResponse(sess *session.Session, msgID uint32, code int32, message, detail string) error {
	errorMsg := oms.messageCodec.CreateErrorMessage(
		msgID,
		0,
		code,
		message,
		detail,
	)

	return oms.SendOrderedMessage(sess, errorMsg)
}

// 发送连接建立响应
func (oms *OrderedMessageSender) SendStartResponse(sess *session.Session, msgID uint32, success bool, err error, heartbeatInterval int32, connectionID string) error {
	response := oms.messageCodec.CreateStartResponse(
		msgID,
		0,
		success,
		nil,
		heartbeatInterval,
		connectionID,
	)

	return oms.SendOrderedMessage(sess, response)
}

// 推送业务数据
func (oms *OrderedMessageSender) PushBusinessData(sess *session.Session, data []byte) error {
	push := &pb.ServerPush{
		Type:    pb.PushType_PUSH_BUSINESS_DATA,
		SeqId:   0,
		Payload: data,
	}

	return oms.SendOrderedMessage(sess, push)
}

// 获取队列统计信息
func (oms *OrderedMessageSender) GetQueueStats(sess *session.Session) map[string]interface{} {
	orderedQueue := sess.GetOrderedQueue()
	if orderedQueue == nil {
		return map[string]interface{}{
			"error": "队列未初始化",
		}
	}

	return orderedQueue.GetQueueStats()
}

// 重新同步会话序列号
func (oms *OrderedMessageSender) ResyncSessionSequence(sess *session.Session, clientAckSeq uint64) {
	orderedQueue := sess.GetOrderedQueue()
	if orderedQueue != nil {
		orderedQueue.ResyncSequence(clientAckSeq)
	}

	log.Printf("会话序列号重同步完成 - 会话: %s, 客户端ACK: %d", sess.ID, clientAckSeq)
}
