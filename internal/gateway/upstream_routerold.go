// Package gateway 提供上游服务路由功能
package gateway

import (
	"context"
	"fmt"
	"strings"

	"gatesvr/internal/upstream"
	pb "gatesvr/proto"
)

// determineUpstreamService 根据业务请求确定目标上游服务类型
func (s *Server) determineUpstreamService(businessReq *pb.BusinessRequest) upstream.ServiceType {
	action := strings.ToLower(businessReq.Action)

	// Hello服务：处理登录、认证相关请求
	if isHelloAction(action) {
		return upstream.ServiceTypeHello
	}

	// Zone服务：处理区域功能相关请求
	if isZoneAction(action) {
		return upstream.ServiceTypeZone
	}

	// 默认使用Business服务
	return upstream.ServiceTypeBusiness
}

// isHelloAction 判断是否为Hello服务相关操作
func isHelloAction(action string) bool {
	helloActions := []string{
		"hello", "logout", "status", "heartbeat", "data_sync",
	}

	for _, helloAction := range helloActions {
		if action == helloAction {
			return true
		}
	}
	return false
}

// isZoneAction 判断是否为Zone服务相关操作
func isZoneAction(action string) bool {
	zoneActions := []string{
		"zone", "echo", "calculate", "time",
	}

	for _, zoneAction := range zoneActions {
		if action == zoneAction || strings.Contains(action, zoneAction) {
			return true
		}
	}
	return false
}

// callUpstreamService 调用指定类型的上游服务
func (s *Server) callUpstreamService(ctx context.Context, serviceType upstream.ServiceType, req *pb.UpstreamRequest) (*pb.UpstreamResponse, error) {
	// 如果使用新的上游管理器
	if s.upstreamManager != nil {
		return s.upstreamManager.CallService(ctx, serviceType, req)
	}

	return nil, fmt.Errorf("没有可用的上游服务连接")
}

// getUpstreamServiceInfo 获取上游服务信息（用于日志和监控）
func (s *Server) getUpstreamServiceInfo(serviceType upstream.ServiceType) string {
	if s.upstreamServices != nil {
		service, err := s.upstreamServices.GetService(serviceType)
		if err == nil && len(service.Addresses) > 0 {
			return fmt.Sprintf("%s(%s)", serviceType, service.Addresses[0])
		}
	}

	return fmt.Sprintf("%s(未配置)", serviceType)
}
