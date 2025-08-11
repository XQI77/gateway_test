// Package upstream 提供基于OpenID的上游服务路由管理
package upstream

import (
	"context"
	"fmt"
	"log"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "gatesvr/proto"
)

// OpenIDBasedRouter 基于OpenID的上游服务路由器
type OpenIDBasedRouter struct {
	zoneServices *ZoneBasedUpstreamServices
	connections  map[string]*grpc.ClientConn // address -> connection
	mu           sync.RWMutex
	ctx          context.Context
	cancel       context.CancelFunc
}

// NewOpenIDBasedRouter 创建基于OpenID的路由器
func NewOpenIDBasedRouter() *OpenIDBasedRouter {
	ctx, cancel := context.WithCancel(context.Background())

	return &OpenIDBasedRouter{
		zoneServices: NewZoneBasedUpstreamServices(),
		connections:  make(map[string]*grpc.ClientConn),
		ctx:          ctx,
		cancel:       cancel,
	}
}

// RegisterUpstream 注册上游服务实例
func (r *OpenIDBasedRouter) RegisterUpstream(address, zoneID string) error {
	// 验证zoneID格式
	if !ValidateZoneID(zoneID) {
		return fmt.Errorf("invalid zone_id format: %s, expected 001-006", zoneID)
	}

	// 创建gRPC客户端连接
	client, err := r.createClient(address)
	if err != nil {
		return fmt.Errorf("failed to create client for %s: %w", address, err)
	}

	// 注册实例
	err = r.zoneServices.RegisterInstance(address, zoneID, client)
	if err != nil {
		return fmt.Errorf("failed to register instance: %w", err)
	}

	log.Printf("上游服务已注册 - Zone: %s, Address: %s", zoneID, address)
	return nil
}

// RouteByOpenID 根据OpenID路由到对应的上游服务
func (r *OpenIDBasedRouter) RouteByOpenID(ctx context.Context, openID string, req *pb.UpstreamRequest) (*pb.UpstreamResponse, error) {
	// 1. 根据OpenID计算ZoneID
	zoneID, err := GetZoneByOpenID(openID)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate zone for openid %s: %w", openID, err)
	}

	// 2. 获取该大区的上游服务实例
	instance, err := r.zoneServices.GetInstanceByZone(zoneID)
	if err != nil {
		return nil, fmt.Errorf("no upstream service available for zone %s: %w", zoneID, err)
	}

	// 3. 更新服务实例活跃时间
	r.zoneServices.UpdateInstanceLastSeen(zoneID)

	// 4. 调用上游服务
	log.Printf("路由请求 - OpenID: %s -> Zone: %s -> Address: %s, Action: %s", 
		openID, zoneID, instance.Address, req.Action)

	response, err := instance.Client.ProcessRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("upstream service call failed for zone %s: %w", zoneID, err)
	}

	return response, nil
}

// createClient 创建新的gRPC客户端连接
func (r *OpenIDBasedRouter) createClient(address string) (pb.UpstreamServiceClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 检查是否已存在连接
	if conn, exists := r.connections[address]; exists {
		return pb.NewUpstreamServiceClient(conn), nil
	}

	// 创建新的gRPC连接
	conn, err := grpc.Dial(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to upstream service %s: %w", address, err)
	}

	// 创建客户端
	client := pb.NewUpstreamServiceClient(conn)

	// 保存连接
	r.connections[address] = conn

	log.Printf("已连接到上游服务: %s", address)
	return client, nil
}

// GetInstanceByZone 根据大区ID获取服务实例
func (r *OpenIDBasedRouter) GetInstanceByZone(zoneID string) (*UpstreamInstance, error) {
	return r.zoneServices.GetInstanceByZone(zoneID)
}

// GetAllInstances 获取所有注册的服务实例
func (r *OpenIDBasedRouter) GetAllInstances() map[string]*UpstreamInstance {
	return r.zoneServices.GetAllInstances()
}

// IsZoneAvailable 检查指定大区是否有可用的服务实例
func (r *OpenIDBasedRouter) IsZoneAvailable(zoneID string) bool {
	return r.zoneServices.IsZoneAvailable(zoneID)
}

// RemoveUpstream 移除上游服务实例
func (r *OpenIDBasedRouter) RemoveUpstream(zoneID string) {
	// 获取实例信息
	instance, err := r.zoneServices.GetInstanceByZone(zoneID)
	if err != nil {
		return
	}

	// 关闭gRPC连接
	r.mu.Lock()
	if conn, exists := r.connections[instance.Address]; exists {
		conn.Close()
		delete(r.connections, instance.Address)
	}
	r.mu.Unlock()

	// 移除实例
	r.zoneServices.RemoveInstance(zoneID)
	log.Printf("上游服务已移除 - Zone: %s, Address: %s", zoneID, instance.Address)
}

// Close 关闭所有连接
func (r *OpenIDBasedRouter) Close() error {
	r.cancel()

	r.mu.Lock()
	defer r.mu.Unlock()

	for address, conn := range r.connections {
		if err := conn.Close(); err != nil {
			log.Printf("关闭上游服务连接失败 %s: %v", address, err)
		}
	}

	r.connections = make(map[string]*grpc.ClientConn)

	log.Printf("上游服务路由器已关闭")
	return nil
}

// GetStats 获取统计信息
func (r *OpenIDBasedRouter) GetStats() map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	connectionStats := make(map[string]string)
	for address := range r.connections {
		connectionStats[address] = "connected"
	}

	stats := r.zoneServices.GetStats()
	stats["connections"] = connectionStats
	stats["connection_count"] = len(r.connections)

	return stats
}

// ValidateOpenID 验证OpenID是否有效并可路由
func (r *OpenIDBasedRouter) ValidateOpenID(openID string) (string, error) {
	zoneID, err := GetZoneByOpenID(openID)
	if err != nil {
		return "", err
	}

	if !r.IsZoneAvailable(zoneID) {
		return "", fmt.Errorf("zone %s has no available upstream service", zoneID)
	}

	return zoneID, nil
}