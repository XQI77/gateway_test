// Package gateway 提供HTTP API处理功能
package gateway

import (
	"encoding/json"
	"gatesvr/internal/session"
	"net/http"
	"time"
)

// 处理健康检查
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":             "healthy",
		"active_connections": s.sessionManager.GetSessionCount(),
		"timestamp":          time.Now().Unix(),
	})
}

// 处理统计信息
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	sessions := s.sessionManager.GetAllSessions()

	stats := make([]map[string]interface{}, len(sessions))
	for i, sess := range sessions {
		sessionStats := map[string]interface{}{
			"session_id":    sess.ID,
			"create_time":   sess.CreateTime.Unix(),
			"last_activity": sess.LastActivity.Unix(),
			"pending_count": s.sessionManager.GetPendingCount(sess.ID),
			"ordered_queue": s.orderedSender.GetQueueStats(sess),
			"remote_addr":   sess.Connection.RemoteAddr().String(),
		}

		// 添加有序队列统计信息
		if queueStats := s.orderedSender.GetQueueStats(sess); queueStats != nil {
			sessionStats["ordered_queue"] = queueStats
		}

		stats[i] = sessionStats
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_sessions": len(sessions),
		"sessions":       stats,
		"timestamp":      time.Now().Unix(),
	})
}

// 获取当前队列配置
func (s *Server) handleGetQueueConfig(w http.ResponseWriter, r *http.Request) {
	// 获取当前环境变量配置
	config := session.LoadQueueConfigFromEnv()

	response := map[string]interface{}{
		"queue_configuration": map[string]interface{}{
			"current_config": config,
			"environment_variables": map[string]string{
				"QUEUE_ENABLE_ASYNC_SEND":   "启用/禁用异步发送 (true/false)",
				"QUEUE_SEND_WORKER_COUNT":   "发送工作协程数量 (1-100)",
				"QUEUE_SEND_QUEUE_SIZE":     "发送队列大小 (100-10000)",
				"QUEUE_MAX_QUEUE_SIZE":      "最大队列大小 (100-10000)",
				"QUEUE_BATCH_TIMEOUT_MS":    "批量发送超时毫秒数 (1-1000)",
				"QUEUE_MAX_RETRIES":         "最大重试次数 (0-10)",
				"QUEUE_CLEANUP_INTERVAL_MS": "清理间隔毫秒数 (1000-60000)",
				"QUEUE_ENABLE_DEBUG_LOG":    "启用调试日志 (true/false)",
				"QUEUE_ENABLE_METRICS":      "启用指标监控 (true/false)",
			},
			"configuration_examples": map[string]interface{}{
				"high_performance": map[string]string{
					"QUEUE_ENABLE_ASYNC_SEND": "true",
					"QUEUE_SEND_WORKER_COUNT": "8",
					"QUEUE_SEND_QUEUE_SIZE":   "2000",
					"QUEUE_MAX_QUEUE_SIZE":    "2000",
					"description":             "高性能配置，适用于大并发场景",
				},
				"conservative": map[string]string{
					"QUEUE_ENABLE_ASYNC_SEND": "true",
					"QUEUE_SEND_WORKER_COUNT": "2",
					"QUEUE_SEND_QUEUE_SIZE":   "500",
					"QUEUE_MAX_QUEUE_SIZE":    "500",
					"description":             "保守配置，适用于内存受限环境",
				},
				"sync_fallback": map[string]string{
					"QUEUE_ENABLE_ASYNC_SEND": "false",
					"description":             "同步模式，用于调试或兼容性",
				},
			},
		},
		"current_sessions_status": s.getSessionsAsyncStatus(),
		"timestamp":               time.Now().Unix(),
	}

	json.NewEncoder(w).Encode(response)
}

// 设置队列配置（仅影响新创建的会话）
func (s *Server) handleSetQueueConfig(w http.ResponseWriter, r *http.Request) {
	var configUpdate map[string]interface{}

	if err := json.NewDecoder(r.Body).Decode(&configUpdate); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	response := map[string]interface{}{
		"message":         "配置更新请求已接收",
		"note":            "环境变量更改需要重启服务才能生效，或仅影响新创建的会话",
		"received_config": configUpdate,
		"recommendations": []string{
			"生产环境建议使用环境变量而非API设置配置",
			"配置更改后监控 /queue/async-stats 确认效果",
			"如需立即生效，请重启服务",
		},
		"timestamp": time.Now().Unix(),
	}

	json.NewEncoder(w).Encode(response)
}

// 获取当前会话的异步发送状态
func (s *Server) getSessionsAsyncStatus() map[string]interface{} {
	sessions := s.sessionManager.GetAllSessions()

	var asyncCount, syncCount int

	for _, sess := range sessions {
		if orderedQueue := sess.GetOrderedQueue(); orderedQueue != nil {
			stats := orderedQueue.GetQueueStats()
			if asyncEnabled, ok := stats["async_enabled"].(bool); ok && asyncEnabled {
				asyncCount++
			} else {
				syncCount++
			}
		}
	}

	return map[string]interface{}{
		"total_sessions":      len(sessions),
		"async_enabled_count": asyncCount,
		"sync_enabled_count":  syncCount,
		"async_adoption_rate": func() float64 {
			if len(sessions) == 0 {
				return 0
			}
			return float64(asyncCount) / float64(len(sessions)) * 100
		}(),
	}
}

// 处理客户端时延查询
func (s *Server) handleClientLatency(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	response := map[string]interface{}{
		"client_latency": map[string]interface{}{
			"description": "客户端到网关的往返时延统计",
			"note":        "此信息需要从客户端获取，当前显示服务端处理时延",
		},
		"timestamp": time.Now().Unix(),
	}

	json.NewEncoder(w).Encode(response)
}

// 处理过载保护状态查询
func (s *Server) handleOverloadProtection(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.overloadProtector == nil {
		response := map[string]interface{}{
			"error":       "过载保护器未初始化",
			"description": "系统未启用过载保护功能",
			"timestamp":   time.Now().Unix(),
		}
		json.NewEncoder(w).Encode(response)
		return
	}

	stats := s.overloadProtector.GetStats()

	response := map[string]interface{}{
		"overload_protection_status": stats,
		"protection_levels": map[string]interface{}{
			"connection_level": map[string]interface{}{
				"current_connections":      stats["current_connections"],
				"max_connections":          stats["max_connections"],
				"connection_usage_percent": float64(stats["current_connections"].(int64)) / float64(stats["max_connections"].(int)) * 100,
				"connections_rejected":     stats["connections_rejected"],
				"status": func() string {
					usage := float64(stats["current_connections"].(int64)) / float64(stats["max_connections"].(int)) * 100
					if usage > 95 {
						return "critical"
					} else if usage > 80 {
						return "warning"
					}
					return "normal"
				}(),
			},
			"qps_level": map[string]interface{}{
				"current_qps": stats["current_qps"],
				"max_qps":     stats["max_qps"],
				"qps_usage_percent": func() float64 {
					if stats["max_qps"].(int) == 0 {
						return 0
					}
					return stats["current_qps"].(float64) / float64(stats["max_qps"].(int)) * 100
				}(),
				"requests_rejected": stats["requests_rejected"],
				"status": func() string {
					if stats["max_qps"].(int) == 0 {
						return "disabled"
					}
					usage := stats["current_qps"].(float64) / float64(stats["max_qps"].(int)) * 100
					if usage > 95 {
						return "critical"
					} else if usage > 80 {
						return "warning"
					}
					return "normal"
				}(),
			},
			"upstream_level": map[string]interface{}{
				"upstream_active":        stats["upstream_active"],
				"max_upstream":           stats["max_upstream"],
				"upstream_usage_percent": float64(stats["upstream_active"].(int64)) / float64(stats["max_upstream"].(int)) * 100,
				"upstream_rejected":      stats["upstream_rejected"],
				"status": func() string {
					usage := float64(stats["upstream_active"].(int64)) / float64(stats["max_upstream"].(int)) * 100
					if usage > 95 {
						return "critical"
					} else if usage > 80 {
						return "warning"
					}
					return "normal"
				}(),
			},
		},
		"overall_status": func() string {
			connectionUsage := float64(stats["current_connections"].(int64)) / float64(stats["max_connections"].(int)) * 100
			var qpsUsage float64
			if stats["max_qps"].(int) > 0 {
				qpsUsage = stats["current_qps"].(float64) / float64(stats["max_qps"].(int)) * 100
			}
			upstreamUsage := float64(stats["upstream_active"].(int64)) / float64(stats["max_upstream"].(int)) * 100

			maxUsage := connectionUsage
			if qpsUsage > maxUsage {
				maxUsage = qpsUsage
			}
			if upstreamUsage > maxUsage {
				maxUsage = upstreamUsage
			}

			if maxUsage > 95 {
				return "critical"
			} else if maxUsage > 80 {
				return "warning"
			}
			return "healthy"
		}(),
		"recommendations": func() []string {
			var recommendations []string
			connectionUsage := float64(stats["current_connections"].(int64)) / float64(stats["max_connections"].(int)) * 100
			var qpsUsage float64
			if stats["max_qps"].(int) > 0 {
				qpsUsage = stats["current_qps"].(float64) / float64(stats["max_qps"].(int)) * 100
			}
			upstreamUsage := float64(stats["upstream_active"].(int64)) / float64(stats["max_upstream"].(int)) * 100

			if connectionUsage > 90 {
				recommendations = append(recommendations, "连接数接近上限，考虑增加最大连接数或优化连接管理")
			}
			if qpsUsage > 90 {
				recommendations = append(recommendations, "QPS接近上限，考虑增加最大QPS或优化请求处理性能")
			}
			if upstreamUsage > 90 {
				recommendations = append(recommendations, "上游并发接近上限，考虑增加上游并发数或优化上游服务性能")
			}
			if stats["connections_rejected"].(int64) > 0 {
				recommendations = append(recommendations, "有连接被拒绝，考虑调整连接保护策略")
			}
			if stats["requests_rejected"].(int64) > 0 {
				recommendations = append(recommendations, "有请求被拒绝，考虑调整QPS保护策略")
			}
			if stats["upstream_rejected"].(int64) > 0 {
				recommendations = append(recommendations, "有上游请求被拒绝，考虑调整上游保护策略")
			}
			if len(recommendations) == 0 {
				recommendations = append(recommendations, "过载保护运行正常，无需调整")
			}
			return recommendations
		}(),
		"configuration": map[string]interface{}{
			"enabled":                      stats["enabled"],
			"uptime_seconds":               stats["uptime_seconds"],
			"connection_warning_threshold": "80% of max_connections",
			"qps_warning_threshold":        "80% of max_qps",
			"upstream_warning_threshold":   "80% of max_upstream",
		},
		"timestamp": time.Now().Unix(),
	}

	json.NewEncoder(w).Encode(response)
}
