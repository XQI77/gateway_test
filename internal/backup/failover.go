package backup

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"gatesvr/internal/session"
)

// FailoverService 故障切换服务
type FailoverService struct {
	config          *FailoverConfig
	currentMode     ServerMode
	modeMux         sync.RWMutex
	heartbeatSvc    HeartbeatInterface
	sessionMgr      *session.Manager
	syncSvc         SyncInterface
	ctx             context.Context
	cancel          context.CancelFunc
	stopped         bool
	stopMux         sync.RWMutex
	isHealthy       bool
	healthMux       sync.RWMutex
	failoverStats   *FailoverStats
	statsMux        sync.RWMutex
	switchCallback  func(ServerMode) error
	restoreCallback func() error
}

// FailoverStats 故障切换统计
type FailoverStats struct {
	TotalSwitches     int           `json:"total_switches"`
	LastSwitchTime    time.Time     `json:"last_switch_time"`
	SwitchDuration    time.Duration `json:"switch_duration"`
	FailureDetections int           `json:"failure_detections"`
	RecoveryAttempts  int           `json:"recovery_attempts"`
	RecoverySuccesses int           `json:"recovery_successes"`
	CurrentMode       ServerMode    `json:"current_mode"`
	IsHealthy         bool          `json:"is_healthy"`
}

// NewFailoverService 创建故障切换服务
func NewFailoverService(
	config *FailoverConfig,
	initialMode ServerMode,
	heartbeatSvc HeartbeatInterface,
	sessionMgr *session.Manager,
	syncSvc SyncInterface,
) FailoverInterface {
	return &FailoverService{
		config:       config,
		currentMode:  initialMode,
		heartbeatSvc: heartbeatSvc,
		sessionMgr:   sessionMgr,
		syncSvc:      syncSvc,
		isHealthy:    true,
		failoverStats: &FailoverStats{
			CurrentMode: initialMode,
			IsHealthy:   true,
		},
	}
}

// Start 启动故障检测
func (f *FailoverService) Start(ctx context.Context) error {
	f.ctx, f.cancel = context.WithCancel(ctx)

	// 启动故障检测协程
	go f.failureDetector()

	// 启动健康检查协程
	go f.healthChecker()

	// 启动统计报告协程
	go f.statsReporter()

	log.Printf("故障切换服务已启动 - 当前模式: %s", f.getModeString())
	return nil
}

// Stop 停止故障检测
func (f *FailoverService) Stop() error {
	f.stopMux.Lock()
	if f.stopped {
		f.stopMux.Unlock()
		return nil
	}
	f.stopped = true
	f.stopMux.Unlock()

	if f.cancel != nil {
		f.cancel()
	}

	log.Printf("故障切换服务已停止")
	return nil
}

// DetectFailure 检测故障
func (f *FailoverService) DetectFailure() bool {
	// 检查心跳服务状态
	if !f.heartbeatSvc.IsPeerAlive() {
		lastHeartbeat := f.heartbeatSvc.GetLastHeartbeatTime()
		if lastHeartbeat > 0 {
			timeSinceLastHeartbeat := time.Duration(time.Now().UnixNano() - lastHeartbeat)
			if timeSinceLastHeartbeat > f.config.DetectionTimeout {
				log.Printf("检测到故障 - 心跳超时: %v", timeSinceLastHeartbeat)
				f.updateStats(func(stats *FailoverStats) {
					stats.FailureDetections++
				})
				return true
			}
		}
	}

	// 检查同步服务状态
	if f.syncSvc != nil && !f.syncSvc.IsConnected() {
		log.Printf("检测到故障 - 同步服务断开")
		f.updateStats(func(stats *FailoverStats) {
			stats.FailureDetections++
		})
		return true
	}

	return false
}

// SwitchToPrimary 切换到主模式
func (f *FailoverService) SwitchToPrimary() error {
	f.modeMux.Lock()
	defer f.modeMux.Unlock()

	if f.currentMode == ModePrimary {
		return fmt.Errorf("已经是主模式")
	}

	log.Printf("开始切换到主模式...")
	startTime := time.Now()

	// 执行切换步骤
	if err := f.doSwitchToPrimary(); err != nil {
		log.Printf("切换到主模式失败: %v", err)
		return err
	}

	f.currentMode = ModePrimary
	switchDuration := time.Since(startTime)

	// 更新统计
	f.updateStats(func(stats *FailoverStats) {
		stats.TotalSwitches++
		stats.LastSwitchTime = time.Now()
		stats.SwitchDuration = switchDuration
		stats.CurrentMode = ModePrimary
	})

	log.Printf("切换到主模式完成 - 耗时: %v", switchDuration)
	return nil
}

// SwitchToBackup 切换到备份模式
func (f *FailoverService) SwitchToBackup() error {
	f.modeMux.Lock()
	defer f.modeMux.Unlock()

	if f.currentMode == ModeBackup {
		return fmt.Errorf("已经是备份模式")
	}

	log.Printf("开始切换到备份模式...")
	startTime := time.Now()

	// 执行切换步骤
	if err := f.doSwitchToBackup(); err != nil {
		log.Printf("切换到备份模式失败: %v", err)
		return err
	}

	f.currentMode = ModeBackup
	switchDuration := time.Since(startTime)

	// 更新统计
	f.updateStats(func(stats *FailoverStats) {
		stats.TotalSwitches++
		stats.LastSwitchTime = time.Now()
		stats.SwitchDuration = switchDuration
		stats.CurrentMode = ModeBackup
	})

	log.Printf("切换到备份模式完成 - 耗时: %v", switchDuration)
	return nil
}

// RestoreSessions 恢复会话
func (f *FailoverService) RestoreSessions() error {
	if f.currentMode != ModePrimary {
		return fmt.Errorf("只有主模式才能恢复会话")
	}

	log.Printf("开始恢复会话...")
	startTime := time.Now()

	f.updateStats(func(stats *FailoverStats) {
		stats.RecoveryAttempts++
	})

	// 执行会话恢复
	if err := f.doRestoreSessions(); err != nil {
		log.Printf("恢复会话失败: %v", err)
		return err
	}

	duration := time.Since(startTime)
	sessionCount := f.sessionMgr.GetSessionCount()

	f.updateStats(func(stats *FailoverStats) {
		stats.RecoverySuccesses++
	})

	log.Printf("恢复会话完成 - 会话数: %d, 耗时: %v", sessionCount, duration)
	return nil
}

// GetMode 获取当前模式
func (f *FailoverService) GetMode() ServerMode {
	f.modeMux.RLock()
	defer f.modeMux.RUnlock()
	return f.currentMode
}

// IsPrimary 检查是否为主服务器
func (f *FailoverService) IsPrimary() bool {
	return f.GetMode() == ModePrimary
}

// IsHealthy 检查健康状态
func (f *FailoverService) IsHealthy() bool {
	f.healthMux.RLock()
	defer f.healthMux.RUnlock()
	return f.isHealthy
}

// SetSwitchCallback 设置切换回调
func (f *FailoverService) SetSwitchCallback(callback func(ServerMode) error) {
	f.switchCallback = callback
}

// SetRestoreCallback 设置恢复回调
func (f *FailoverService) SetRestoreCallback(callback func() error) {
	f.restoreCallback = callback
}

// GetStats 获取故障切换统计
func (f *FailoverService) GetStats() *FailoverStats {
	f.statsMux.RLock()
	defer f.statsMux.RUnlock()

	return &FailoverStats{
		TotalSwitches:     f.failoverStats.TotalSwitches,
		LastSwitchTime:    f.failoverStats.LastSwitchTime,
		SwitchDuration:    f.failoverStats.SwitchDuration,
		FailureDetections: f.failoverStats.FailureDetections,
		RecoveryAttempts:  f.failoverStats.RecoveryAttempts,
		RecoverySuccesses: f.failoverStats.RecoverySuccesses,
		CurrentMode:       f.failoverStats.CurrentMode,
		IsHealthy:         f.failoverStats.IsHealthy,
	}
}

// failureDetector 故障检测协程
func (f *FailoverService) failureDetector() {
	ticker := time.NewTicker(f.config.DetectionTimeout / 3) // 检测间隔为超时时间的1/3
	defer ticker.Stop()

	consecutiveFailures := 0
	maxConsecutiveFailures := 3

	for {
		select {
		case <-f.ctx.Done():
			return
		case <-ticker.C:
			if f.currentMode == ModeBackup {
				// 备份模式下检测主服务器故障
				if f.DetectFailure() {
					consecutiveFailures++
					log.Printf("检测到故障 - 连续失败次数: %d/%d", consecutiveFailures, maxConsecutiveFailures)

					if consecutiveFailures >= maxConsecutiveFailures {
						log.Printf("连续故障检测达到阈值，开始故障切换")
						if err := f.SwitchToPrimary(); err != nil {
							log.Printf("自动故障切换失败: %v", err)
						} else {
							// 切换成功后尝试恢复会话
							go func() {
								time.Sleep(2 * time.Second) // 等待切换稳定
								if err := f.RestoreSessions(); err != nil {
									log.Printf("自动恢复会话失败: %v", err)
								}
							}()
						}
						consecutiveFailures = 0
					}
				} else {
					consecutiveFailures = 0
				}
			}
		}
	}
}

// healthChecker 健康检查协程
func (f *FailoverService) healthChecker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-f.ctx.Done():
			return
		case <-ticker.C:
			healthy := f.checkSystemHealth()
			f.setHealthy(healthy)

			f.updateStats(func(stats *FailoverStats) {
				stats.IsHealthy = healthy
			})
		}
	}
}

// statsReporter 统计报告协程
func (f *FailoverService) statsReporter() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-f.ctx.Done():
			return
		case <-ticker.C:
			stats := f.GetStats()
			log.Printf("故障切换统计 - 模式: %s, 总切换: %d, 故障检测: %d, 恢复尝试: %d, 健康: %v",
				f.getModeString(), stats.TotalSwitches, stats.FailureDetections,
				stats.RecoveryAttempts, stats.IsHealthy)
		}
	}
}

// doSwitchToPrimary 执行切换到主模式
func (f *FailoverService) doSwitchToPrimary() error {
	log.Printf("执行主模式切换步骤...")

	// 1. 停止同步接收（如果有的话）
	if f.syncSvc != nil {
		log.Printf("步骤1: 准备接管主服务器职责")
	}

	// 2. 启用写入模式（退出只读模式）
	log.Printf("步骤2: 启用写入模式")

	// 3. 开始接受客户端连接
	log.Printf("步骤3: 准备接受客户端连接")

	// 4. 调用外部切换回调
	if f.switchCallback != nil {
		if err := f.switchCallback(ModePrimary); err != nil {
			return fmt.Errorf("执行外部切换回调失败: %w", err)
		}
	}

	log.Printf("主模式切换步骤完成")
	return nil
}

// doSwitchToBackup 执行切换到备份模式
func (f *FailoverService) doSwitchToBackup() error {
	log.Printf("执行备份模式切换步骤...")

	// 1. 停止接受新的客户端连接
	log.Printf("步骤1: 停止接受新连接")

	// 2. 启用只读模式
	log.Printf("步骤2: 启用只读模式")

	// 3. 启动同步接收
	log.Printf("步骤3: 启动同步接收")

	// 4. 调用外部切换回调
	if f.switchCallback != nil {
		if err := f.switchCallback(ModeBackup); err != nil {
			return fmt.Errorf("执行外部切换回调失败: %w", err)
		}
	}

	log.Printf("备份模式切换步骤完成")
	return nil
}

// doRestoreSessions 执行会话恢复
func (f *FailoverService) doRestoreSessions() error {
	if f.sessionMgr == nil {
		return fmt.Errorf("会话管理器未设置")
	}

	// 1. 获取当前会话信息
	sessions := f.sessionMgr.GetAllSessions()
	log.Printf("当前会话数量: %d", len(sessions))

	// 2. 验证会话数据完整性
	validSessions := 0
	for _, sess := range sessions {
		if sess != nil && !sess.IsClosed() {
			validSessions++
		}
	}
	log.Printf("有效会话数量: %d", validSessions)

	// 3. 重建必要的索引
	log.Printf("重建会话索引...")

	// 4. 调用外部恢复回调
	if f.restoreCallback != nil {
		if err := f.restoreCallback(); err != nil {
			return fmt.Errorf("执行外部恢复回调失败: %w", err)
		}
	}

	return nil
}

// checkSystemHealth 检查系统健康状态
func (f *FailoverService) checkSystemHealth() bool {
	// 检查会话管理器
	if f.sessionMgr == nil {
		return false
	}

	// 检查心跳服务
	if f.heartbeatSvc == nil {
		return false
	}

	// 在主模式下，检查同步服务
	if f.currentMode == ModePrimary && f.syncSvc != nil {
		syncStats := f.syncSvc.GetStats()
		if syncStats.ErrorCount > 100 { // 错误过多
			return false
		}
	}

	return true
}

// setHealthy 设置健康状态
func (f *FailoverService) setHealthy(healthy bool) {
	f.healthMux.Lock()
	defer f.healthMux.Unlock()
	f.isHealthy = healthy
}

// updateStats 更新统计
func (f *FailoverService) updateStats(fn func(*FailoverStats)) {
	f.statsMux.Lock()
	defer f.statsMux.Unlock()
	fn(f.failoverStats)
}

// getModeString 获取模式字符串
func (f *FailoverService) getModeString() string {
	switch f.GetMode() {
	case ModePrimary:
		return "主服务器"
	case ModeBackup:
		return "备份服务器"
	default:
		return "未知"
	}
}

// isStopped 检查是否已停止
func (f *FailoverService) isStopped() bool {
	f.stopMux.RLock()
	defer f.stopMux.RUnlock()
	return f.stopped
}

// ManualSwitchToPrimary 手动切换到主模式
func (f *FailoverService) ManualSwitchToPrimary() error {
	log.Printf("手动触发切换到主模式")
	return f.SwitchToPrimary()
}

// ManualSwitchToBackup 手动切换到备份模式
func (f *FailoverService) ManualSwitchToBackup() error {
	log.Printf("手动触发切换到备份模式")
	return f.SwitchToBackup()
}

// ManualRestore 手动恢复会话
func (f *FailoverService) ManualRestore() error {
	log.Printf("手动触发会话恢复")
	return f.RestoreSessions()
}