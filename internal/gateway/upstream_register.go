package gateway

import (
	"context"
	"fmt"
	"log"

	pb "gatesvr/proto"
)

// 处理upstream服务注册请求
func (s *Server) RegisterUpstream(ctx context.Context, req *pb.UpstreamRegisterRequest) (*pb.UpstreamRegisterResponse, error) {
	log.Printf("收到upstream服务注册请求 - Zone: %s, Address: %s, Name: %s",
		req.ZoneId, req.Address, req.ServiceName)
	if req.Address == "" {
		return &pb.UpstreamRegisterResponse{
			Success:   false,
			Message:   "地址不能为空",
			ErrorCode: "INVALID_ADDRESS",
		}, nil
	}

	if req.ZoneId == "" {
		return &pb.UpstreamRegisterResponse{
			Success:   false,
			Message:   "大区ID不能为空",
			ErrorCode: "INVALID_ZONE_ID",
		}, nil
	}

	// 注册upstream服务
	err := s.registerUpstreamService(req.Address, req.ZoneId)
	if err != nil {
		log.Printf("upstream服务注册失败 - Zone: %s, Address: %s, 错误: %v",
			req.ZoneId, req.Address, err)

		return &pb.UpstreamRegisterResponse{
			Success:   false,
			Message:   fmt.Sprintf("注册失败: %v", err),
			ErrorCode: "REGISTER_FAILED",
		}, nil
	}

	log.Printf("upstream服务注册成功 - Zone: %s, Address: %s",
		req.ZoneId, req.Address)

	return &pb.UpstreamRegisterResponse{
		Success: true,
		Message: "注册成功",
	}, nil
}
