// Package gateway 提供简化的性能跟踪器，替代复杂的performance.go
package gateway

import (
	"sync"
	"sync/atomic"
	"time"
)

// SimpleTracker 简化的性能跟踪器（替代复杂的PerformanceTracker）
type SimpleTracker struct {
	startTime         time.Time
	totalConnections  int64
	activeConnections int64
	totalRequests     int64
	totalErrors       int64

	// 轻量级QPS计算 - 使用计数器而非时间戳数组
	lastQPSTime   time.Time // 上次QPS计算时间
	lastTotalReqs int64     // 上次QPS计算时的总请求数
	recentQPS     float64   // 最近的QPS值
	qpsMutex      sync.RWMutex
}

// NewSimpleTracker 创建简化的性能跟踪器
func NewSimpleTracker() *SimpleTracker {
	now := time.Now()
	return &SimpleTracker{
		startTime:     now,
		lastQPSTime:   now,
		lastTotalReqs: 0,
		recentQPS:     0.0,
	}
}

// RecordConnection 记录新连接
func (st *SimpleTracker) RecordConnection() {
	atomic.AddInt64(&st.totalConnections, 1)
	atomic.AddInt64(&st.activeConnections, 1)
}

// RecordDisconnection 记录连接断开
func (st *SimpleTracker) RecordDisconnection() {
	atomic.AddInt64(&st.activeConnections, -1)
}

// RecordRequest 记录请求
func (st *SimpleTracker) RecordRequest() {
	atomic.AddInt64(&st.totalRequests, 1)
}

// RecordError 记录错误
func (st *SimpleTracker) RecordError() {
	atomic.AddInt64(&st.totalErrors, 1)
}

// GetBasicStats 获取基础统计信息
func (st *SimpleTracker) GetBasicStats() map[string]interface{} {
	uptime := time.Since(st.startTime)
	totalReqs := atomic.LoadInt64(&st.totalRequests)
	totalErrs := atomic.LoadInt64(&st.totalErrors)
	activeConns := atomic.LoadInt64(&st.activeConnections)
	totalConns := atomic.LoadInt64(&st.totalConnections)

	// 计算瞬时QPS（轻量级方法）
	instantQPS := st.calculateLightweightQPS()

	successRate := float64(totalReqs-totalErrs) / float64(totalReqs) * 100
	if totalReqs == 0 {
		successRate = 100
	}

	return map[string]interface{}{
		"uptime_seconds":     uptime.Seconds(),
		"total_connections":  totalConns,
		"active_connections": activeConns,
		"total_requests":     totalReqs,
		"total_errors":       totalErrs,
		"qps":                instantQPS,
		"success_rate":       successRate,
	}
}

// calculateLightweightQPS 轻量级QPS计算 - 每5秒更新一次
func (st *SimpleTracker) calculateLightweightQPS() float64 {
	now := time.Now()
	currentTotalReqs := atomic.LoadInt64(&st.totalRequests)

	st.qpsMutex.Lock()
	defer st.qpsMutex.Unlock()

	// 如果距离上次计算超过3秒，重新计算QPS
	timeSinceLastCalc := now.Sub(st.lastQPSTime).Seconds()
	if timeSinceLastCalc >= 3.0 {
		// 计算这段时间内的请求增量
		requestsDelta := currentTotalReqs - st.lastTotalReqs

		// 计算QPS = 请求增量 / 时间间隔
		if timeSinceLastCalc > 0 {
			st.recentQPS = float64(requestsDelta) / timeSinceLastCalc
		}

		// 更新记录
		st.lastQPSTime = now
		st.lastTotalReqs = currentTotalReqs
	}

	// 如果服务刚启动（少于5秒），使用累计平均值
	if time.Since(st.startTime).Seconds() < 5.0 {
		uptime := time.Since(st.startTime).Seconds()
		if uptime > 0 {
			return float64(currentTotalReqs) / uptime
		}
	}

	return st.recentQPS
}

// GetStats 兼容原有接口，返回基础统计
func (st *SimpleTracker) GetStats() map[string]interface{} {
	return st.GetBasicStats()
}
