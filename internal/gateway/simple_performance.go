// Package gateway 提供简化的性能监控功能，使用pprof替代复杂监控
package gateway

import (
	"encoding/json"
	"net/http"
	"runtime"
)

// handleSimplePerformance 处理简化的性能监控请求
func (s *Server) handleSimplePerformance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	// 获取基础统计信息
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	
	// 获取性能追踪器统计
	trackerStats := s.performanceTracker.GetBasicStats()
	
	response := map[string]interface{}{
		"basic_stats": trackerStats,
		"memory": map[string]interface{}{
			"alloc_mb":      m.Alloc / 1024 / 1024,
			"total_alloc_mb": m.TotalAlloc / 1024 / 1024,
			"sys_mb":        m.Sys / 1024 / 1024,
			"gc_count":      m.NumGC,
		},
		"goroutines": map[string]interface{}{
			"count": runtime.NumGoroutine(),
		},
		"pprof_endpoints": map[string]string{
			"cpu_profile":    "/debug/pprof/profile",
			"memory_profile": "/debug/pprof/heap",
			"goroutine_profile": "/debug/pprof/goroutine",
			"block_profile":  "/debug/pprof/block",
			"mutex_profile":  "/debug/pprof/mutex",
			"allocs_profile": "/debug/pprof/allocs",
			"trace":         "/debug/pprof/trace",
			"web_ui":        "/debug/pprof/",
		},
		"usage_examples": map[string]string{
			"cpu_analysis":    "go tool pprof http://localhost:8080/debug/pprof/profile?seconds=30",
			"memory_analysis": "go tool pprof http://localhost:8080/debug/pprof/heap",
			"goroutine_analysis": "go tool pprof http://localhost:8080/debug/pprof/goroutine",
			"web_interface":   "浏览器访问 http://localhost:8080/debug/pprof/",
		},
		"note": "复杂的性能监控已被Go pprof替代，获得更准确的性能分析",
	}
	
	json.NewEncoder(w).Encode(response)
}