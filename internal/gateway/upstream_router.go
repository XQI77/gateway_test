// Package gateway 提供基于OpenID的上游服务路由功能
package gateway

import (
	"context"
	"fmt"
	pb "gatesvr/proto"
	"log"
)

func (s *Server) callUpstreamService(ctx context.Context, openID string, req *pb.UpstreamRequest) (*pb.UpstreamResponse, error) {
	// 验证OpenID
	if openID == "" {
		return nil, fmt.Errorf("openID cannot be empty for upstream routing")
	}

	// 使用新的基于OpenID的路由器
	if s.upstreamRouter == nil {
		return nil, fmt.Errorf("upstream router not initialized")
	}

	// 路由到对应的上游服务实例
	response, err := s.upstreamRouter.RouteByOpenID(ctx, openID, req)
	if err != nil {
		log.Printf("上游服务调用失败 - OpenID: %s, Action: %s, 错误: %v", openID, req.Action, err)
		return nil, fmt.Errorf("upstream service call failed: %w", err)
	}

	log.Printf("上游服务调用成功 - OpenID: %s, Action: %s, Code: %d",
		openID, req.Action, response.Code)

	return response, nil
}

// 获取上游服务信息
func (s *Server) getUpstreamServiceInfo(openID string) string {
	if s.upstreamRouter == nil {
		return "路由器未初始化"
	}

	zoneID, err := s.upstreamRouter.ValidateOpenID(openID)
	if err != nil {
		return fmt.Sprintf("无效OpenID(%s): %v", openID, err)
	}

	instance, err := s.upstreamRouter.GetInstanceByZone(zoneID)
	if err != nil {
		return fmt.Sprintf("Zone %s: 无可用服务", zoneID)
	}

	return fmt.Sprintf("Zone %s: %s", zoneID, instance.Address)
}

func (s *Server) registerUpstreamService(address, zoneID string) error {
	if s.upstreamRouter == nil {
		return fmt.Errorf("upstream router not initialized")
	}

	err := s.upstreamRouter.RegisterUpstream(address, zoneID)
	if err != nil {
		return fmt.Errorf("failed to register upstream service: %w", err)
	}

	log.Printf("上游服务注册成功 - Zone: %s, Address: %s", zoneID, address)
	return nil
}

// 获取上游服务统计信息
func (s *Server) getUpstreamStats() map[string]interface{} {
	if s.upstreamRouter == nil {
		return map[string]interface{}{
			"error": "upstream router not initialized",
		}
	}

	return s.upstreamRouter.GetStats()
}

func (s *Server) validateUpstreamRouting() error {
	if s.upstreamRouter == nil {
		return fmt.Errorf("upstream router not initialized")
	}
	instances := s.upstreamRouter.GetAllInstances()
	if len(instances) == 0 {
		log.Printf("警告: 没有注册的上游服务实例")
		return nil
	}
	log.Printf("上游路由验证通过 - 已注册 %d 个服务实例:", len(instances))
	for zoneID, instance := range instances {
		log.Printf("  Zone %s: %s", zoneID, instance.Address)
	}

	return nil
}
