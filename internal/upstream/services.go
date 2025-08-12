// Package upstream 提供多上游服务管理
package upstream

import (
	"fmt"
	"sync"
)

type ServiceType string

const (
	ServiceTypeHello    ServiceType = "hello"
	ServiceTypeBusiness ServiceType = "business"
	ServiceTypeZone     ServiceType = "zone"
)

type ServiceInfo struct {
	Type      ServiceType `json:"type"`
	Addresses []string    `json:"addresses"`
	Enabled   bool        `json:"enabled"`
}

type UpstreamServices struct {
	services map[ServiceType]*ServiceInfo
	mu       sync.RWMutex
}

func NewUpstreamServices() *UpstreamServices {
	return &UpstreamServices{
		services: make(map[ServiceType]*ServiceInfo),
	}
}

func (us *UpstreamServices) AddService(serviceType ServiceType, addresses []string) {
	us.mu.Lock()
	defer us.mu.Unlock()

	us.services[serviceType] = &ServiceInfo{
		Type:      serviceType,
		Addresses: addresses,
		Enabled:   true,
	}
}

func (us *UpstreamServices) GetService(serviceType ServiceType) (*ServiceInfo, error) {
	us.mu.RLock()
	defer us.mu.RUnlock()

	service, exists := us.services[serviceType]
	if !exists {
		return nil, fmt.Errorf("服务类型 %s 不存在", serviceType)
	}

	if !service.Enabled {
		return nil, fmt.Errorf("服务类型 %s 已禁用", serviceType)
	}

	return service, nil
}

func (us *UpstreamServices) GetAllServices() map[ServiceType]*ServiceInfo {
	us.mu.RLock()
	defer us.mu.RUnlock()

	result := make(map[ServiceType]*ServiceInfo)
	for k, v := range us.services {
		result[k] = &ServiceInfo{
			Type:      v.Type,
			Addresses: make([]string, len(v.Addresses)),
			Enabled:   v.Enabled,
		}
		copy(result[k].Addresses, v.Addresses)
	}

	return result
}

func (us *UpstreamServices) UpdateService(serviceType ServiceType, addresses []string) error {
	us.mu.Lock()
	defer us.mu.Unlock()

	service, exists := us.services[serviceType]
	if !exists {
		return fmt.Errorf("服务类型 %s 不存在", serviceType)
	}

	service.Addresses = addresses
	return nil
}

func (us *UpstreamServices) EnableService(serviceType ServiceType) error {
	us.mu.Lock()
	defer us.mu.Unlock()

	service, exists := us.services[serviceType]
	if !exists {
		return fmt.Errorf("服务类型 %s 不存在", serviceType)
	}

	service.Enabled = true
	return nil
}

func (us *UpstreamServices) DisableService(serviceType ServiceType) error {
	us.mu.Lock()
	defer us.mu.Unlock()

	service, exists := us.services[serviceType]
	if !exists {
		return fmt.Errorf("服务类型 %s 不存在", serviceType)
	}

	service.Enabled = false
	return nil
}

func (us *UpstreamServices) RemoveService(serviceType ServiceType) {
	us.mu.Lock()
	defer us.mu.Unlock()

	delete(us.services, serviceType)
}

func (us *UpstreamServices) GetServiceCount() int {
	us.mu.RLock()
	defer us.mu.RUnlock()

	return len(us.services)
}

func (us *UpstreamServices) GetEnabledServiceCount() int {
	us.mu.RLock()
	defer us.mu.RUnlock()

	count := 0
	for _, service := range us.services {
		if service.Enabled {
			count++
		}
	}
	return count
}

func (us *UpstreamServices) IsServiceAvailable(serviceType ServiceType) bool {
	us.mu.RLock()
	defer us.mu.RUnlock()

	service, exists := us.services[serviceType]
	if !exists {
		return false
	}

	return service.Enabled && len(service.Addresses) > 0
}

func (us *UpstreamServices) GetStats() map[string]interface{} {
	us.mu.RLock()
	defer us.mu.RUnlock()

	stats := make(map[string]interface{})
	serviceStats := make(map[string]interface{})

	for serviceType, service := range us.services {
		serviceStats[string(serviceType)] = map[string]interface{}{
			"enabled":       service.Enabled,
			"address_count": len(service.Addresses),
			"addresses":     service.Addresses,
		}
	}

	stats["services"] = serviceStats
	stats["total_services"] = len(us.services)
	stats["enabled_services"] = us.GetEnabledServiceCount()

	return stats
}
