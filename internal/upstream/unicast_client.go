package upstream

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "gatesvr/proto"
)

// UnicastClient 单播推送客户端
type UnicastClient struct {
	conn     *grpc.ClientConn
	client   pb.GatewayServiceClient
	gateAddr string
}

// NewUnicastClient 创建新的单播推送客户端
func NewUnicastClient(gateAddr string) *UnicastClient {
	return &UnicastClient{
		gateAddr: gateAddr,
	}
}

// Connect 连接到网关服务
func (c *UnicastClient) Connect() error {
	conn, err := grpc.Dial(c.gateAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("连接网关服务失败: %w", err)
	}

	c.conn = conn
	c.client = pb.NewGatewayServiceClient(conn)

	log.Printf("已连接到网关服务: %s", c.gateAddr)
	return nil
}

// Close 关闭连接
func (c *UnicastClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// PushToClient 向指定客户端推送消息
func (c *UnicastClient) PushToClient(ctx context.Context, targetType, targetID, msgType, title, content string, data []byte) error {
	if c.client == nil {
		return fmt.Errorf("客户端未连接")
	}

	req := &pb.UnicastPushRequest{
		TargetType: targetType,
		TargetId:   targetID,
		MsgType:    msgType,
		Title:      title,
		Content:    content,
		Data:       data,
	}

	resp, err := c.client.PushToClient(ctx, req)
	if err != nil {
		return fmt.Errorf("gRPC调用失败: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("推送失败: %s (错误码: %s)", resp.Message, resp.ErrorCode)
	}

	log.Printf("单播推送成功 - 目标: %s:%s, 消息: %s", targetType, targetID, title)
	return nil
}

// PushToGID 推送到指定GID
func (c *UnicastClient) PushToGID(ctx context.Context, gid int64, msgType, title, content string, data []byte) error {
	return c.PushToClient(ctx, "gid", fmt.Sprintf("%d", gid), msgType, title, content, data)
}

// PushToOpenID 推送到指定OpenID
func (c *UnicastClient) PushToOpenID(ctx context.Context, openID, msgType, title, content string, data []byte) error {
	return c.PushToClient(ctx, "openid", openID, msgType, title, content, data)
}

// PushToOpenIDWithSyncHint 推送到指定OpenID，带同步提示
func (c *UnicastClient) PushToOpenIDWithSyncHint(ctx context.Context, openID, msgType, title, content string, data []byte, syncHint pb.NotifySyncHint, bindClientSeqId uint64) error {
	if c.client == nil {
		return fmt.Errorf("客户端未连接")
	}

	req := &pb.UnicastPushRequest{
		TargetType:        "openid",
		TargetId:          openID,
		MsgType:           msgType,
		Title:             title,
		Content:           content,
		Data:              data,
		SyncHint:          syncHint,
		BindClientSeqId:   bindClientSeqId,
	}

	resp, err := c.client.PushToClient(ctx, req)
	if err != nil {
		return fmt.Errorf("gRPC调用失败: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("推送失败: %s (错误码: %s)", resp.Message, resp.ErrorCode)
	}

	log.Printf("带同步提示的推送成功 - OpenID: %s, 同步提示: %v, 绑定序列号: %d, 消息: %s", 
		openID, syncHint, bindClientSeqId, title)
	return nil
}

// PushToSession 推送到指定Session
func (c *UnicastClient) PushToSession(ctx context.Context, sessionID, msgType, title, content string, data []byte) error {
	return c.PushToClient(ctx, "session", sessionID, msgType, title, content, data)
}

// BroadcastToClients 广播消息到所有在线客户端
func (c *UnicastClient) BroadcastToClients(ctx context.Context, msgType, title, content string, data []byte, metadata map[string]string) error {
	if c.client == nil {
		return fmt.Errorf("客户端未连接")
	}

	req := &pb.BroadcastRequest{
		MsgType:  msgType,
		Title:    title,
		Content:  content,
		Data:     data,
		Metadata: metadata,
	}

	resp, err := c.client.BroadcastToClients(ctx, req)
	if err != nil {
		return fmt.Errorf("广播gRPC调用失败: %w", err)
	}

	log.Printf("广播消息发送成功 - 发送给 %d 个客户端, 消息: %s", resp.SentCount, title)
	return nil
}

// DemoUnicastPush 演示单播推送功能（保留用于兼容性）
func (c *UnicastClient) DemoUnicastPush() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	log.Println("=== 演示单播推送功能 ===")

	// 1. 推送到指定GID
	err := c.PushToGID(ctx, 12345, "system", "系统通知", "这是一条系统推送消息", []byte("test data"))
	if err != nil {
		log.Printf("推送到GID失败: %v", err)
	}

	// 2. 推送到指定OpenID
	err = c.PushToOpenID(ctx, "user123", "personal", "个人消息", "您有新的消息", nil)
	if err != nil {
		log.Printf("推送到OpenID失败: %v", err)
	}

	log.Println("=== 单播推送演示完成 ===")
}
