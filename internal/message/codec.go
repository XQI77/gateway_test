// Package message 提供消息编解码和处理功能
package message

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	pb "gatesvr/proto"

	"google.golang.org/protobuf/proto"
)

const (
	MessageHeaderSize = 4
	MaxMessageSize    = 1024 * 1024
)

type ReadLatencyCallback func(time.Duration)

type MessageCodec struct {
	readLatencyCallback ReadLatencyCallback
}

func NewMessageCodec() *MessageCodec {
	return &MessageCodec{}
}

func (c *MessageCodec) SetReadLatencyCallback(callback ReadLatencyCallback) {
	c.readLatencyCallback = callback
}

func (c *MessageCodec) EncodeClientRequest(req *pb.ClientRequest) ([]byte, error) {

	data, err := proto.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化ClientRequest失败: %w", err)
	}

	if len(data) > MaxMessageSize {
		return nil, fmt.Errorf("消息太大: %d 字节, 最大允许: %d 字节", len(data), MaxMessageSize)
	}

	return data, nil
}

func (c *MessageCodec) DecodeClientRequest(data []byte) (*pb.ClientRequest, error) {
	req := &pb.ClientRequest{}
	if err := proto.Unmarshal(data, req); err != nil {
		return nil, fmt.Errorf("反序列化ClientRequest失败: %w", err)
	}
	return req, nil
}

func (c *MessageCodec) EncodeServerPush(push *pb.ServerPush) ([]byte, error) {
	data, err := proto.Marshal(push)
	if err != nil {
		return nil, fmt.Errorf("序列化ServerPush失败: %w", err)
	}

	// 检查消息大小
	if len(data) > MaxMessageSize {
		return nil, fmt.Errorf("消息太大: %d 字节, 最大允许: %d 字节", len(data), MaxMessageSize)
	}

	return data, nil
}

func (c *MessageCodec) DecodeServerPush(data []byte) (*pb.ServerPush, error) {
	push := &pb.ServerPush{}
	if err := proto.Unmarshal(data, push); err != nil {
		return nil, fmt.Errorf("反序列化ServerPush失败: %w", err)
	}
	return push, nil
}

func (c *MessageCodec) EncodeClientAck(ack *pb.ClientAck) ([]byte, error) {
	data, err := proto.Marshal(ack)
	if err != nil {
		return nil, fmt.Errorf("序列化ClientAck失败: %w", err)
	}
	return data, nil
}

// DecodeClientAck 解码客户端ACK消息
func (c *MessageCodec) DecodeClientAck(data []byte) (*pb.ClientAck, error) {
	ack := &pb.ClientAck{}
	if err := proto.Unmarshal(data, ack); err != nil {
		return nil, fmt.Errorf("反序列化ClientAck失败: %w", err)
	}
	return ack, nil
}

// 从io.Reader中读取一个完整的消息
func (c *MessageCodec) ReadMessage(reader io.Reader) ([]byte, error) {
	readStartTime := time.Now()
	defer func() {
		if c.readLatencyCallback != nil {
			c.readLatencyCallback(time.Since(readStartTime))
		}
	}()

	header := make([]byte, MessageHeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, fmt.Errorf("读取消息头失败: %w", err)
	}

	messageLen := binary.BigEndian.Uint32(header)

	if messageLen > MaxMessageSize {
		return nil, fmt.Errorf("消息长度过大: %d 字节, 最大允许: %d 字节", messageLen, MaxMessageSize)
	}

	messageData := make([]byte, messageLen)
	if _, err := io.ReadFull(reader, messageData); err != nil {
		return nil, fmt.Errorf("读取消息体失败: %w", err)
	}

	return messageData, nil
}

// 向io.Writer写入一个消息
func (c *MessageCodec) WriteMessage(writer io.Writer, data []byte) error {
	if len(data) > MaxMessageSize {
		return fmt.Errorf("消息太大: %d 字节, 最大允许: %d 字节", len(data), MaxMessageSize)
	}

	buf := bytes.NewBuffer(make([]byte, 0, MessageHeaderSize+len(data)))

	if err := binary.Write(buf, binary.BigEndian, uint32(len(data))); err != nil {
		return fmt.Errorf("写入消息长度失败: %w", err)
	}

	if _, err := buf.Write(data); err != nil {
		return fmt.Errorf("写入消息数据失败: %w", err)
	}

	if _, err := writer.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("发送消息失败: %w", err)
	}

	return nil
}

func (c *MessageCodec) CreateHeartbeatRequest(msgID uint32, seqID uint64, openID string) *pb.ClientRequest {
	heartbeat := &pb.HeartbeatRequest{
		ClientTimestamp: getCurrentTimestamp(),
	}

	payload, _ := proto.Marshal(heartbeat)

	return &pb.ClientRequest{
		MsgId:   msgID,
		SeqId:   seqID,
		Type:    pb.RequestType_REQUEST_HEARTBEAT,
		Payload: payload,
		Openid:  openID,
	}
}

func (c *MessageCodec) CreateHeartbeatResponse(msgID uint32, seqID uint64, clientTimestamp int64) *pb.ServerPush {
	heartbeat := &pb.HeartbeatResponse{
		ClientTimestamp: clientTimestamp,
		ServerTimestamp: getCurrentTimestamp(),
	}

	payload, _ := proto.Marshal(heartbeat)

	return &pb.ServerPush{
		MsgId:   msgID,
		SeqId:   seqID,
		Type:    pb.PushType_PUSH_HEARTBEAT_RESP,
		Payload: payload,
	}
}

func (c *MessageCodec) CreateBusinessRequest(msgID uint32, seqID uint64, action string, params map[string]string, data []byte, openID string) *pb.ClientRequest {
	business := &pb.BusinessRequest{
		Action: action,
		Params: params,
		Data:   data,
	}

	payload, _ := proto.Marshal(business)

	return &pb.ClientRequest{
		MsgId:   msgID,
		SeqId:   seqID,
		Type:    pb.RequestType_REQUEST_BUSINESS,
		Payload: payload,
		Openid:  openID,
	}
}

func (c *MessageCodec) CreateBusinessResponse(msgID uint32, seqID uint64, code int32, message string, data []byte) *pb.ServerPush {
	business := &pb.BusinessResponse{
		Code:    code,
		Message: message,
		Data:    data,
	}

	payload, _ := proto.Marshal(business)

	return &pb.ServerPush{
		MsgId:   msgID,
		SeqId:   seqID,
		Type:    pb.PushType_PUSH_BUSINESS_DATA,
		Payload: payload,
	}
}

func (c *MessageCodec) CreateAckMessage(seqID uint64, openID string) *pb.ClientRequest {
	ack := &pb.ClientAck{
		AckedSeqId: seqID,
	}

	payload, _ := proto.Marshal(ack)

	return &pb.ClientRequest{
		MsgId:   0,
		SeqId:   0,
		Type:    pb.RequestType_REQUEST_ACK,
		Payload: payload,
		Openid:  openID,
	}
}

func (c *MessageCodec) CreateErrorMessage(msgID uint32, seqID uint64, code int32, message, detail string) *pb.ServerPush {
	errorMsg := &pb.ErrorMessage{
		ErrorCode:    code,
		ErrorMessage: message,
		Detail:       detail,
	}

	payload, _ := proto.Marshal(errorMsg)

	return &pb.ServerPush{
		MsgId:   msgID,
		SeqId:   seqID,
		Type:    pb.PushType_PUSH_ERROR,
		Payload: payload,
	}
}

func getCurrentTimestamp() int64 {
	return time.Now().UnixMilli()
}

func (c *MessageCodec) CreateStartResponse(msgID uint32, seqID uint64, success bool, errorMsg *pb.ErrorMessage, heartbeatInterval int32, connectionID string) *pb.ServerPush {
	startResp := &pb.StartResponse{
		Success:           success,
		Error:             errorMsg,
		HeartbeatInterval: heartbeatInterval,
		ConnectionId:      connectionID,
	}

	payload, _ := proto.Marshal(startResp)

	return &pb.ServerPush{
		MsgId:   msgID,
		SeqId:   seqID,
		Type:    pb.PushType_PUSH_START_RESP,
		Payload: payload,
	}
}

func (c *MessageCodec) CreateStartRequest(msgID uint32, seqID uint64, openID, authToken string, lastAckedSeqID uint64) *pb.ClientRequest {
	startReq := &pb.StartRequest{
		Openid:         openID,
		AuthToken:      authToken,
		LastAckedSeqId: lastAckedSeqID,
	}

	payload, _ := proto.Marshal(startReq)

	return &pb.ClientRequest{
		MsgId:   msgID,
		SeqId:   seqID,
		Type:    pb.RequestType_REQUEST_START,
		Payload: payload,
		Openid:  openID,
	}
}

func (c *MessageCodec) CreateStopRequest(msgID uint32, seqID uint64, reason pb.StopRequest_Reason, openID string) *pb.ClientRequest {
	stopReq := &pb.StopRequest{
		Reason: reason,
	}

	payload, _ := proto.Marshal(stopReq)

	return &pb.ClientRequest{
		MsgId:   msgID,
		SeqId:   seqID,
		Type:    pb.RequestType_REQUEST_STOP,
		Payload: payload,
		Openid:  openID,
	}
}
