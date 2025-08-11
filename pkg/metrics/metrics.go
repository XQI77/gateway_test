// Package metrics 提供 Prometheus 监控指标
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// GateServerMetrics 网关服务器监控指标
type GateServerMetrics struct {
	// QPS指标 - 每秒处理的请求数
	qpsCounter prometheus.Counter

	// 吞吐量指标 - 区分上行和下行字节数
	throughputBytes *prometheus.CounterVec

	// 活跃连接数
	activeConnections prometheus.Gauge

	// 队列大小指标 - 下行消息缓存队列长度
	outboundQueueSize *prometheus.GaugeVec

	// 延迟指标 - 请求处理延迟
	requestDuration *prometheus.HistogramVec

	// 错误计数器
	errorCounter *prometheus.CounterVec

	// 过载保护指标
	connectionsRejected *prometheus.CounterVec // 被拒绝的连接数
	requestsRejected    *prometheus.CounterVec // 被拒绝的请求数
	upstreamRejected    *prometheus.CounterVec // 被拒绝的上游请求数
	currentQPS          prometheus.Gauge        // 当前QPS
	upstreamConcurrent  prometheus.Gauge        // 当前上游并发数
}

// NewGateServerMetrics 创建新的监控指标实例
func NewGateServerMetrics() *GateServerMetrics {
	return &GateServerMetrics{
		// QPS计数器
		qpsCounter: promauto.NewCounter(prometheus.CounterOpts{
			Name: "gatesvr_qps_total",
			Help: "网关每秒处理的请求总数",
		}),

		// 吞吐量计数器，区分方向
		throughputBytes: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gatesvr_throughput_bytes_total",
				Help: "网关的网络吞吐量（字节）",
			},
			[]string{"direction"}, // "inbound" 或 "outbound"
		),

		// 活跃连接数
		activeConnections: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "gatesvr_active_connections",
			Help: "当前活跃的客户端连接数",
		}),

		// 队列大小，按会话ID分组
		outboundQueueSize: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "gatesvr_outbound_queue_size",
				Help: "下行消息缓存队列的当前长度",
			},
			[]string{"session_id"},
		),

		// 请求处理延迟
		requestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gatesvr_request_duration_seconds",
				Help:    "请求处理时间分布",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"request_type"},
		),

		// 错误计数器
		errorCounter: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gatesvr_errors_total",
				Help: "错误总数",
			},
			[]string{"error_type"},
		),

		// 过载保护指标
		connectionsRejected: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gatesvr_connections_rejected_total",
				Help: "被过载保护拒绝的连接总数",
			},
			[]string{"reason"},
		),

		requestsRejected: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gatesvr_requests_rejected_total",
				Help: "被过载保护拒绝的请求总数",
			},
			[]string{"reason"},
		),

		upstreamRejected: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gatesvr_upstream_rejected_total",
				Help: "被过载保护拒绝的上游请求总数",
			},
			[]string{"reason"},
		),

		currentQPS: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "gatesvr_current_qps",
			Help: "当前每秒查询率",
		}),

		upstreamConcurrent: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "gatesvr_upstream_concurrent",
			Help: "当前上游并发请求数",
		}),
	}
}

// IncQPS 增加QPS计数
func (m *GateServerMetrics) IncQPS() {
	m.qpsCounter.Inc()
}

// AddThroughput 增加吞吐量计数
func (m *GateServerMetrics) AddThroughput(direction string, bytes int64) {
	m.throughputBytes.WithLabelValues(direction).Add(float64(bytes))
}

// SetActiveConnections 设置活跃连接数
func (m *GateServerMetrics) SetActiveConnections(count int) {
	m.activeConnections.Set(float64(count))
}

// SetOutboundQueueSize 设置出站队列大小
func (m *GateServerMetrics) SetOutboundQueueSize(sessionID string, size int) {
	m.outboundQueueSize.WithLabelValues(sessionID).Set(float64(size))
}

// ObserveRequestDuration 记录请求处理时间
func (m *GateServerMetrics) ObserveRequestDuration(requestType string, duration time.Duration) {
	m.requestDuration.WithLabelValues(requestType).Observe(duration.Seconds())
}

// IncError 增加错误计数
func (m *GateServerMetrics) IncError(errorType string) {
	m.errorCounter.WithLabelValues(errorType).Inc()
}

// RemoveSession 移除会话相关的指标
func (m *GateServerMetrics) RemoveSession(sessionID string) {
	m.outboundQueueSize.DeleteLabelValues(sessionID)
}

// IncConnectionsRejected 增加被拒绝的连接数
func (m *GateServerMetrics) IncConnectionsRejected(reason string) {
	m.connectionsRejected.WithLabelValues(reason).Inc()
}

// IncRequestsRejected 增加被拒绝的请求数
func (m *GateServerMetrics) IncRequestsRejected(reason string) {
	m.requestsRejected.WithLabelValues(reason).Inc()
}

// IncUpstreamRejected 增加被拒绝的上游请求数
func (m *GateServerMetrics) IncUpstreamRejected(reason string) {
	m.upstreamRejected.WithLabelValues(reason).Inc()
}

// SetCurrentQPS 设置当前QPS
func (m *GateServerMetrics) SetCurrentQPS(qps float64) {
	m.currentQPS.Set(qps)
}

// SetUpstreamConcurrent 设置当前上游并发数
func (m *GateServerMetrics) SetUpstreamConcurrent(count int64) {
	m.upstreamConcurrent.Set(float64(count))
}

// MetricsServer 监控指标服务器
type MetricsServer struct {
	server *http.Server
}

// NewMetricsServer 创建新的监控指标服务器
func NewMetricsServer(addr string) *MetricsServer {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	return &MetricsServer{
		server: &http.Server{
			Addr:    addr,
			Handler: mux,
		},
	}
}

// Start 启动监控服务器
func (s *MetricsServer) Start() error {
	return s.server.ListenAndServe()
}

// Stop 停止监控服务器
func (s *MetricsServer) Stop() error {
	return s.server.Close()
}
