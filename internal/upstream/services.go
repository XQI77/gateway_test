// Package upstream 提供基于大区的上游服务管理
package upstream

import (
	"fmt"
	"sync"
	"time"

	pb "gatesvr/proto"
)

// UpstreamInstance 上游服务实例
type UpstreamInstance struct {
	Address    string                    `json:"address"`    // 服务地址 "ip:port"
	ZoneID     string                    `json:"zone_id"`    // 所属大区 "001"-"006"
	Client     pb.UpstreamServiceClient  `json:"-"`          // gRPC客户端
	Registered time.Time                 `json:"registered"` // 注册时间
	LastSeen   time.Time                 `json:"last_seen"`  // 最后活跃时间
}

// ZoneBasedUpstreamServices 基于大区的上游服务管理器
type ZoneBasedUpstreamServices struct {
	zoneInstances map[string]*UpstreamInstance // zoneID -> instance
	mu            sync.RWMutex
}

// NewZoneBasedUpstreamServices 创建基于大区的上游服务管理器
func NewZoneBasedUpstreamServices() *ZoneBasedUpstreamServices {
	return &ZoneBasedUpstreamServices{
		zoneInstances: make(map[string]*UpstreamInstance),
	}
}

// RegisterInstance 注册上游服务实例
func (zs *ZoneBasedUpstreamServices) RegisterInstance(address, zoneID string, client pb.UpstreamServiceClient) error {
	zs.mu.Lock()
	defer zs.mu.Unlock()

	// 验证zoneID格式
	if len(zoneID) != 3 {
		return fmt.Errorf("invalid zone_id format: %s, expected 3-digit format", zoneID)
	}

	now := time.Now()
	zs.zoneInstances[zoneID] = &UpstreamInstance{
		Address:    address,
		ZoneID:     zoneID,
		Client:     client,
		Registered: now,
		LastSeen:   now,
	}

	return nil
}

// GetInstanceByZone 根据大区ID获取上游服务实例
func (zs *ZoneBasedUpstreamServices) GetInstanceByZone(zoneID string) (*UpstreamInstance, error) {
	zs.mu.RLock()
	defer zs.mu.RUnlock()

	instance, exists := zs.zoneInstances[zoneID]
	if !exists {
		return nil, fmt.Errorf("zone %s no upstream service registered", zoneID)
	}

	return instance, nil
}

// GetAllInstances 获取所有上游服务实例
func (zs *ZoneBasedUpstreamServices) GetAllInstances() map[string]*UpstreamInstance {
	zs.mu.RLock()
	defer zs.mu.RUnlock()

	result := make(map[string]*UpstreamInstance)
	for zoneID, instance := range zs.zoneInstances {
		result[zoneID] = &UpstreamInstance{
			Address:    instance.Address,
			ZoneID:     instance.ZoneID,
			Client:     instance.Client,
			Registered: instance.Registered,
			LastSeen:   instance.LastSeen,
		}
	}

	return result
}

// UpdateInstanceLastSeen 更新服务实例最后活跃时间
func (zs *ZoneBasedUpstreamServices) UpdateInstanceLastSeen(zoneID string) {
	zs.mu.Lock()
	defer zs.mu.Unlock()

	if instance, exists := zs.zoneInstances[zoneID]; exists {
		instance.LastSeen = time.Now()
	}
}

// RemoveInstance 移除服务实例
func (zs *ZoneBasedUpstreamServices) RemoveInstance(zoneID string) {
	zs.mu.Lock()
	defer zs.mu.Unlock()

	delete(zs.zoneInstances, zoneID)
}

// GetInstanceCount 获取注册的服务实例数量
func (zs *ZoneBasedUpstreamServices) GetInstanceCount() int {
	zs.mu.RLock()
	defer zs.mu.RUnlock()

	return len(zs.zoneInstances)
}

// IsZoneAvailable 检查指定大区是否有可用的服务实例
func (zs *ZoneBasedUpstreamServices) IsZoneAvailable(zoneID string) bool {
	zs.mu.RLock()
	defer zs.mu.RUnlock()

	_, exists := zs.zoneInstances[zoneID]
	return exists
}

// GetStats 获取统计信息
func (zs *ZoneBasedUpstreamServices) GetStats() map[string]interface{} {
	zs.mu.RLock()
	defer zs.mu.RUnlock()

	stats := make(map[string]interface{})
	instanceStats := make(map[string]interface{})

	for zoneID, instance := range zs.zoneInstances {
		instanceStats[zoneID] = map[string]interface{}{
			"address":    instance.Address,
			"registered": instance.Registered.Format("2006-01-02 15:04:05"),
			"last_seen":  instance.LastSeen.Format("2006-01-02 15:04:05"),
		}
	}

	stats["instances"] = instanceStats
	stats["total_instances"] = len(zs.zoneInstances)
	stats["available_zones"] = len(zs.zoneInstances)

	return stats
}