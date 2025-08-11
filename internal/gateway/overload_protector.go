// Package gateway 提供过载保护功能
package gateway

import (
	"gatesvr/pkg/metrics"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// 过载保护配置
type OverloadConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`

	// 连接保护配置
	MaxConnections             int `yaml:"max_connections" json:"max_connections"`
	ConnectionWarningThreshold int `yaml:"connection_warning_threshold" json:"connection_warning_threshold"`

	// QPS保护配置
	MaxQPS              int `yaml:"max_qps" json:"max_qps"`
	QPSWarningThreshold int `yaml:"qps_warning_threshold" json:"qps_warning_threshold"`
	QPSWindowSeconds    int `yaml:"qps_window_seconds" json:"qps_window_seconds"`

	// 上游保护配置
	MaxUpstreamConcurrent    int           `yaml:"max_upstream_concurrent" json:"max_upstream_concurrent"`
	UpstreamTimeout          time.Duration `yaml:"upstream_timeout" json:"upstream_timeout"`
	UpstreamWarningThreshold int           `yaml:"upstream_warning_threshold" json:"upstream_warning_threshold"`
}

func DefaultOverloadConfig() *OverloadConfig {
	return &OverloadConfig{
		Enabled:                    true,
		MaxConnections:             1000,
		ConnectionWarningThreshold: 800,
		MaxQPS:                     2000,
		QPSWarningThreshold:        1600,
		QPSWindowSeconds:           10,
		MaxUpstreamConcurrent:      100,
		UpstreamTimeout:            30 * time.Second,
		UpstreamWarningThreshold:   80,
	}
}

// QPS滑动窗口统计
type SlidingWindow struct {
	windowSize time.Duration
	buckets    []int64
	bucketSize time.Duration
	current    int
	startTime  time.Time
	mutex      sync.RWMutex
}

// 创建滑动窗口
func NewSlidingWindow(windowSize time.Duration, bucketCount int) *SlidingWindow {
	return &SlidingWindow{
		windowSize: windowSize,
		buckets:    make([]int64, bucketCount),
		bucketSize: windowSize / time.Duration(bucketCount),
		startTime:  time.Now(),
	}
}

func (sw *SlidingWindow) Add(count int64) {
	sw.mutex.Lock()
	defer sw.mutex.Unlock()

	now := time.Now()
	elapsed := now.Sub(sw.startTime)

	bucketIndex := int(elapsed/sw.bucketSize) % len(sw.buckets)

	if elapsed >= sw.windowSize {
		for i := range sw.buckets {
			sw.buckets[i] = 0
		}
		sw.startTime = now
		bucketIndex = 0
	} else {
		for elapsed >= sw.windowSize && sw.current != bucketIndex {
			sw.buckets[sw.current] = 0
			sw.current = (sw.current + 1) % len(sw.buckets)
		}
	}

	sw.current = bucketIndex
	sw.buckets[bucketIndex] += count
}

// 获取窗口内总计数
func (sw *SlidingWindow) Sum() int64 {
	sw.mutex.RLock()
	defer sw.mutex.RUnlock()

	var sum int64
	for _, count := range sw.buckets {
		sum += count
	}
	return sum
}

// 获取当前速率
func (sw *SlidingWindow) Rate() float64 {
	sum := sw.Sum()
	return float64(sum) / sw.windowSize.Seconds()
}

type OverloadProtector struct {
	config  *OverloadConfig
	metrics *metrics.GateServerMetrics

	currentConnections int64

	qpsWindow *SlidingWindow

	upstreamActive int64

	connectionsRejected int64
	requestsRejected    int64
	upstreamRejected    int64

	startTime time.Time

	mutex sync.RWMutex
}

// 创建过载保护器
func NewOverloadProtector(config *OverloadConfig, metricsInstance *metrics.GateServerMetrics) *OverloadProtector {
	if config == nil {
		config = DefaultOverloadConfig()
	}

	protector := &OverloadProtector{
		config:    config,
		metrics:   metricsInstance,
		qpsWindow: NewSlidingWindow(time.Duration(config.QPSWindowSeconds)*time.Second, 10),
		startTime: time.Now(),
	}

	if config.Enabled {
		log.Printf("过载保护器已启用 - 最大连接: %d, 最大QPS: %d, 最大上游并发: %d",
			config.MaxConnections, config.MaxQPS, config.MaxUpstreamConcurrent)
	} else {
		log.Printf("过载保护器已禁用")
	}

	return protector
}

// 检查是否允许新连接
func (op *OverloadProtector) AllowNewConnection() bool {
	if !op.config.Enabled {
		return true
	}

	current := atomic.LoadInt64(&op.currentConnections)
	maxConns := int64(op.config.MaxConnections)
	warningThreshold := int64(op.config.ConnectionWarningThreshold)

	if current >= maxConns {
		atomic.AddInt64(&op.connectionsRejected, 1)
		if op.metrics != nil {
			op.metrics.IncConnectionsRejected("max_connections_exceeded")
		}
		log.Printf("连接被拒绝 - 当前连接: %d, 最大连接: %d", current, maxConns)
		return false
	}

	if current >= warningThreshold {
		log.Printf("连接数警告 - 当前连接: %d, 警告阈值: %d", current, warningThreshold)
	}

	return true
}

// 检查是否允许新请求
func (op *OverloadProtector) AllowNewRequest() bool {
	if !op.config.Enabled {
		op.qpsWindow.Add(1) // 仍然统计QPS，即使不启用保护
		return true
	}

	op.qpsWindow.Add(1)

	currentQPS := op.qpsWindow.Rate()
	if op.metrics != nil {
		op.metrics.SetCurrentQPS(currentQPS)
	}

	maxQPS := float64(op.config.MaxQPS)
	warningThreshold := float64(op.config.QPSWarningThreshold)

	if currentQPS >= maxQPS {
		atomic.AddInt64(&op.requestsRejected, 1)
		if op.metrics != nil {
			op.metrics.IncRequestsRejected("max_qps_exceeded")
		}
		log.Printf("请求被拒绝 - 当前QPS: %.2f, 最大QPS: %.2f", currentQPS, maxQPS)
		return false
	}

	if currentQPS >= warningThreshold {
		log.Printf("QPS警告 - 当前QPS: %.2f, 警告阈值: %.2f", currentQPS, warningThreshold)
		time.Sleep(time.Millisecond)
	}

	return true
}

// 检查是否允许上游请求
func (op *OverloadProtector) AllowUpstreamRequest() bool {
	if !op.config.Enabled {
		return true
	}

	current := atomic.LoadInt64(&op.upstreamActive)
	maxUpstream := int64(op.config.MaxUpstreamConcurrent)
	warningThreshold := int64(op.config.UpstreamWarningThreshold)

	if current >= maxUpstream {
		atomic.AddInt64(&op.upstreamRejected, 1)
		if op.metrics != nil {
			op.metrics.IncUpstreamRejected("max_upstream_concurrent_exceeded")
		}
		log.Printf("上游请求被拒绝 - 当前并发: %d, 最大并发: %d", current, maxUpstream)
		return false
	}

	if current >= warningThreshold {
		log.Printf("上游并发警告 - 当前并发: %d, 警告阈值: %d", current, warningThreshold)
	}

	return true
}

// 连接开始时调用
func (op *OverloadProtector) OnConnectionStart() {
	current := atomic.AddInt64(&op.currentConnections, 1)
	if op.metrics != nil {
		op.metrics.SetActiveConnections(int(current))
	}
}

// 连接结束时调用
func (op *OverloadProtector) OnConnectionEnd() {
	current := atomic.AddInt64(&op.currentConnections, -1)
	if current < 0 {
		atomic.StoreInt64(&op.currentConnections, 0)
		current = 0
	}
	if op.metrics != nil {
		op.metrics.SetActiveConnections(int(current))
	}
}

// 上游请求开始时调用
func (op *OverloadProtector) OnUpstreamStart() {
	current := atomic.AddInt64(&op.upstreamActive, 1)
	if op.metrics != nil {
		op.metrics.SetUpstreamConcurrent(current)
	}
}

// 上游请求结束时调用
func (op *OverloadProtector) OnUpstreamEnd() {
	current := atomic.AddInt64(&op.upstreamActive, -1)
	if current < 0 {
		atomic.StoreInt64(&op.upstreamActive, 0)
		current = 0
	}
	if op.metrics != nil {
		op.metrics.SetUpstreamConcurrent(current)
	}
}

// 获取过载保护统计信息
func (op *OverloadProtector) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"enabled":              op.config.Enabled,
		"current_connections":  atomic.LoadInt64(&op.currentConnections),
		"max_connections":      op.config.MaxConnections,
		"current_qps":          op.qpsWindow.Rate(),
		"max_qps":              op.config.MaxQPS,
		"upstream_active":      atomic.LoadInt64(&op.upstreamActive),
		"max_upstream":         op.config.MaxUpstreamConcurrent,
		"connections_rejected": atomic.LoadInt64(&op.connectionsRejected),
		"requests_rejected":    atomic.LoadInt64(&op.requestsRejected),
		"upstream_rejected":    atomic.LoadInt64(&op.upstreamRejected),
		"uptime_seconds":       int64(time.Since(op.startTime).Seconds()),
	}
}

// 检查连接是否过载
func (op *OverloadProtector) IsConnectionOverloaded() bool {
	if !op.config.Enabled {
		return false
	}
	current := atomic.LoadInt64(&op.currentConnections)
	return current >= int64(op.config.MaxConnections)
}

// 检查QPS是否过载
func (op *OverloadProtector) IsQPSOverloaded() bool {
	if !op.config.Enabled {
		return false
	}
	return op.qpsWindow.Rate() >= float64(op.config.MaxQPS)
}

// 检查上游是否过载
func (op *OverloadProtector) IsUpstreamOverloaded() bool {
	if !op.config.Enabled {
		return false
	}
	current := atomic.LoadInt64(&op.upstreamActive)
	return current >= int64(op.config.MaxUpstreamConcurrent)
}

// 获取当前连接数
func (op *OverloadProtector) GetCurrentConnections() int64 {
	return atomic.LoadInt64(&op.currentConnections)
}

// 获取当前QPS
func (op *OverloadProtector) GetCurrentQPS() float64 {
	return op.qpsWindow.Rate()
}

// 获取当前上游活跃请求数
func (op *OverloadProtector) GetUpstreamActive() int64 {
	return atomic.LoadInt64(&op.upstreamActive)
}

// 重置统计数据
func (op *OverloadProtector) Reset() {
	atomic.StoreInt64(&op.currentConnections, 0)
	atomic.StoreInt64(&op.upstreamActive, 0)
	atomic.StoreInt64(&op.connectionsRejected, 0)
	atomic.StoreInt64(&op.requestsRejected, 0)
	atomic.StoreInt64(&op.upstreamRejected, 0)
	op.qpsWindow = NewSlidingWindow(time.Duration(op.config.QPSWindowSeconds)*time.Second, 10)
	op.startTime = time.Now()
}

// 动态启用/禁用保护
func (op *OverloadProtector) SetEnabled(enabled bool) {
	op.mutex.Lock()
	defer op.mutex.Unlock()

	op.config.Enabled = enabled
	if enabled {
		log.Printf("过载保护已启用")
	} else {
		log.Printf("过载保护已禁用")
	}
}

// 更新配置
func (op *OverloadProtector) UpdateConfig(newConfig *OverloadConfig) {
	op.mutex.Lock()
	defer op.mutex.Unlock()

	if newConfig != nil {
		op.config = newConfig
		if newConfig.QPSWindowSeconds != 0 {
			op.qpsWindow = NewSlidingWindow(time.Duration(newConfig.QPSWindowSeconds)*time.Second, 10)
		}
		log.Printf("过载保护配置已更新 - 最大连接: %d, 最大QPS: %d, 最大上游并发: %d",
			newConfig.MaxConnections, newConfig.MaxQPS, newConfig.MaxUpstreamConcurrent)
	}
}
