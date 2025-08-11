package backup

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"gatesvr/internal/session"
)

type BackupManagerImpl struct {
	config           *BackupConfig
	currentMode      ServerMode
	modeMux          sync.RWMutex
	sessionMgr       *session.Manager
	syncService      SyncInterface
	syncReceiver     SyncReceiver
	heartbeatService HeartbeatInterface
	failoverService  FailoverInterface
	ctx              context.Context
	cancel           context.CancelFunc
	stopped          bool
	stopMux          sync.RWMutex
	serverID         string
	stats            *BackupManagerStats
	statsMux         sync.RWMutex
}

type BackupManagerStats struct {
	StartTime        time.Time   `json:"start_time"`
	CurrentMode      ServerMode  `json:"current_mode"`
	SyncEnabled      bool        `json:"sync_enabled"`
	HeartbeatEnabled bool        `json:"heartbeat_enabled"`
	FailoverEnabled  bool        `json:"failover_enabled"`
	SessionCount     int         `json:"session_count"`
	SyncStats        *SyncStats  `json:"sync_stats"`
	HeartbeatStats   interface{} `json:"heartbeat_stats"`
	FailoverStats    interface{} `json:"failover_stats"`
	LastUpdateTime   time.Time   `json:"last_update_time"`
	IsHealthy        bool        `json:"is_healthy"`
}

func NewBackupManager(config *BackupConfig, serverID string) BackupManager {
	return &BackupManagerImpl{
		config:   config,
		serverID: serverID,
		stats: &BackupManagerStats{
			StartTime:        time.Now(),
			SyncEnabled:      config.Sync.Enabled,
			HeartbeatEnabled: config.Sync.Enabled,
			FailoverEnabled:  config.Sync.Enabled,
			IsHealthy:        true,
		},
	}
}

func (bm *BackupManagerImpl) Start(ctx context.Context) error {
	bm.ctx, bm.cancel = context.WithCancel(ctx)
	bm.currentMode = bm.config.Sync.Mode

	if !bm.config.Sync.Enabled {
		log.Printf("备份功能未启用")
		return nil
	}

	log.Printf("启动备份管理器 - 服务器ID: %s, 模式: %s", bm.serverID, bm.getModeString())

	// 启动同步服务
	if err := bm.startSyncServices(); err != nil {
		return fmt.Errorf("启动同步服务失败: %w", err)
	}

	// 启动心跳服务
	if err := bm.startHeartbeatService(); err != nil {
		return fmt.Errorf("启动心跳服务失败: %w", err)
	}

	// 启动故障切换服务
	if err := bm.startFailoverService(); err != nil {
		return fmt.Errorf("启动故障切换服务失败: %w", err)
	}

	// 启动统计更新协程
	go bm.statsUpdater()

	// 如果是主模式，延迟启动同步发送
	if bm.currentMode == ModePrimary {
		go func() {
			time.Sleep(5 * time.Second) // 等待系统稳定
			bm.startSessionSyncHooks()
		}()
	}

	log.Printf("备份管理器启动完成")
	return nil
}

func (bm *BackupManagerImpl) Stop() error {
	bm.stopMux.Lock()
	if bm.stopped {
		bm.stopMux.Unlock()
		return nil
	}
	bm.stopped = true
	bm.stopMux.Unlock()

	if bm.cancel != nil {
		bm.cancel()
	}

	// 停止各个服务
	if bm.syncService != nil {
		bm.syncService.Stop()
	}
	if bm.syncReceiver != nil {
		bm.syncReceiver.Stop()
	}
	if bm.heartbeatService != nil {
		bm.heartbeatService.Stop()
	}
	if bm.failoverService != nil {
		bm.failoverService.Stop()
	}

	log.Printf("备份管理器已停止")
	return nil
}

func (bm *BackupManagerImpl) GetMode() ServerMode {
	bm.modeMux.RLock()
	defer bm.modeMux.RUnlock()
	return bm.currentMode
}

func (bm *BackupManagerImpl) SwitchMode(mode ServerMode) error {
	if !bm.config.Sync.Enabled {
		return fmt.Errorf("备份功能未启用")
	}

	bm.modeMux.Lock()
	defer bm.modeMux.Unlock()

	if bm.currentMode == mode {
		return fmt.Errorf("已经是%s模式", bm.getModeStringForMode(mode))
	}

	log.Printf("切换模式: %s -> %s", bm.getModeString(), bm.getModeStringForMode(mode))

	if bm.failoverService != nil {
		if mode == ModePrimary {
			return bm.failoverService.SwitchToPrimary()
		} else {
			return bm.failoverService.SwitchToBackup()
		}
	}

	return fmt.Errorf("故障切换服务未初始化")
}

func (bm *BackupManagerImpl) RegisterSessionManager(mgr *session.Manager) {
	bm.sessionMgr = mgr
	log.Printf("会话管理器已注册到备份管理器")
}

func (bm *BackupManagerImpl) GetStats() map[string]interface{} {
	bm.statsMux.RLock()
	defer bm.statsMux.RUnlock()

	stats := map[string]interface{}{
		"server_id":        bm.serverID,
		"current_mode":     bm.getModeString(),
		"sync_enabled":     bm.config.Sync.Enabled,
		"start_time":       bm.stats.StartTime,
		"session_count":    bm.stats.SessionCount,
		"last_update_time": bm.stats.LastUpdateTime,
		"is_healthy":       bm.stats.IsHealthy,
	}

	// 添加各服务统计
	if bm.syncService != nil {
		stats["sync_stats"] = bm.syncService.GetStats()
	}
	if bm.heartbeatService != nil {
		if hbSvc, ok := bm.heartbeatService.(*HeartbeatService); ok {
			stats["heartbeat_stats"] = hbSvc.GetStats()
		}
	}
	if bm.failoverService != nil {
		if foSvc, ok := bm.failoverService.(*FailoverService); ok {
			stats["failover_stats"] = foSvc.GetStats()
		}
	}

	return stats
}

func (bm *BackupManagerImpl) IsHealthy() bool {
	if !bm.config.Sync.Enabled {
		return true // 未启用备份功能时认为健康
	}

	// 检查各个服务的健康状态
	if bm.failoverService != nil {
		return bm.failoverService.IsHealthy()
	}

	return true
}

func (bm *BackupManagerImpl) TriggerSync() error {
	if bm.syncService == nil {
		return fmt.Errorf("同步服务未初始化")
	}

	return bm.syncService.FullSync()
}

func (bm *BackupManagerImpl) TriggerFailover() error {
	if bm.failoverService == nil {
		return fmt.Errorf("故障切换服务未初始化")
	}

	if bm.currentMode == ModeBackup {
		return bm.failoverService.SwitchToPrimary()
	} else {
		return bm.failoverService.SwitchToBackup()
	}
}

func (bm *BackupManagerImpl) startSyncServices() error {
	if bm.currentMode == ModePrimary {
		// 主服务器启动同步发送服务
		if bm.config.SyncAddr != "" {
			bm.syncService = NewSyncServiceWithAddr(&bm.config.Sync, bm.config.SyncAddr)
		} else {
			// 向后兼容，使用旧配置
			bm.syncService = NewSyncService(&bm.config.Sync)
		}
		if err := bm.syncService.Start(bm.ctx); err != nil {
			return fmt.Errorf("启动同步发送服务失败: %w", err)
		}
		log.Printf("同步发送服务已启动")
	}

	if bm.currentMode == ModeBackup {
		// 备份服务器启动同步接收服务
		if bm.config.SyncAddr != "" {
			bm.syncReceiver = newSyncReceiverWithAddr(&bm.config.Sync, bm.sessionMgr, bm.config.SyncAddr)
		} else {
			bm.syncReceiver = newSyncReceiver(&bm.config.Sync, bm.sessionMgr)
		}
		if err := bm.syncReceiver.Start(bm.ctx); err != nil {
			return fmt.Errorf("启动同步接收服务失败: %w", err)
		}
		log.Printf("同步接收服务已启动")
	}

	return nil
}

func (bm *BackupManagerImpl) startHeartbeatService() error {
	// 使用分离的心跳地址
	if bm.config.HeartbeatAddr != "" {
		bm.heartbeatService = NewHeartbeatServiceWithAddr(&bm.config.Sync, bm.currentMode, bm.serverID, bm.sessionMgr, bm.config.HeartbeatAddr)
	} else {
		// 向后兼容，使用旧配置
		bm.heartbeatService = NewHeartbeatService(&bm.config.Sync, bm.currentMode, bm.serverID, bm.sessionMgr)
	}

	if err := bm.heartbeatService.Start(bm.ctx); err != nil {
		return fmt.Errorf("启动心跳服务失败: %w", err)
	}
	log.Printf("心跳服务已启动")
	return nil
}

func (bm *BackupManagerImpl) startFailoverService() error {
	bm.failoverService = NewFailoverService(
		&bm.config.Failover,
		bm.currentMode,
		bm.heartbeatService,
		bm.sessionMgr,
		bm.syncService,
	)

	// 设置切换回调
	if foSvc, ok := bm.failoverService.(*FailoverService); ok {
		foSvc.SetSwitchCallback(bm.onModeSwitch)
		foSvc.SetRestoreCallback(bm.onSessionRestore)
	}

	if err := bm.failoverService.Start(bm.ctx); err != nil {
		return fmt.Errorf("启动故障切换服务失败: %w", err)
	}
	log.Printf("故障切换服务已启动")
	return nil
}

// 启动会话同步钩子
func (bm *BackupManagerImpl) startSessionSyncHooks() {
	if bm.sessionMgr == nil || bm.syncService == nil {
		log.Printf("会话管理器或同步服务未就绪，跳过同步钩子启动")
		return
	}

	log.Printf("启动会话同步钩子")
	// 这里可以添加会话变更监听逻辑
	// 由于当前的SessionManager没有变更通知机制，暂时使用定期同步
	go bm.periodicSync()
}

// 定期同步
func (bm *BackupManagerImpl) periodicSync() {
	ticker := time.NewTicker(bm.config.Sync.SyncTimeout * 2)
	defer ticker.Stop()

	for {
		select {
		case <-bm.ctx.Done():
			return
		case <-ticker.C:
			if bm.currentMode == ModePrimary && bm.syncService != nil {
				bm.syncAllSessions()
			}
		}
	}
}

// 同步所有会话
func (bm *BackupManagerImpl) syncAllSessions() {
	if bm.sessionMgr == nil {
		return
	}

	sessions := bm.sessionMgr.GetAllSessions()
	syncCount := 0

	for _, sess := range sessions {
		if sess != nil && !sess.IsClosed() {
			if err := bm.syncService.SyncSession(sess.ID, sess); err != nil {
				//log.Printf("同步会话失败: %s, 错误: %v", sess.ID, err)
			} else {
				syncCount++
			}
		}
	}

	if syncCount > 0 {
		log.Printf("定期同步完成 - 同步会话数: %d", syncCount)
	}
}

func (bm *BackupManagerImpl) statsUpdater() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-bm.ctx.Done():
			return
		case <-ticker.C:
			bm.updateStats()
		}
	}
}

func (bm *BackupManagerImpl) updateStats() {
	bm.statsMux.Lock()
	defer bm.statsMux.Unlock()

	bm.stats.CurrentMode = bm.currentMode
	bm.stats.LastUpdateTime = time.Now()
	bm.stats.IsHealthy = bm.IsHealthy()

	if bm.sessionMgr != nil {
		bm.stats.SessionCount = bm.sessionMgr.GetSessionCount()
	}

	if bm.syncService != nil {
		bm.stats.SyncStats = bm.syncService.GetStats()
	}
}

func (bm *BackupManagerImpl) onModeSwitch(newMode ServerMode) error {
	log.Printf("执行模式切换回调: %s", bm.getModeStringForMode(newMode))

	// 更新当前模式
	bm.modeMux.Lock()
	bm.currentMode = newMode
	bm.modeMux.Unlock()

	// 根据新模式调整服务
	if newMode == ModePrimary {
		// 切换到主模式：启动同步发送，停止接收
		if bm.syncReceiver != nil {
			if err := bm.syncReceiver.Stop(); err != nil {
				log.Printf("停止同步接收服务失败: %v", err)
			}
			bm.syncReceiver = nil
		}

		if bm.syncService == nil {
			// 使用分离的同步地址
			if bm.config.SyncAddr != "" {
				bm.syncService = NewSyncServiceWithAddr(&bm.config.Sync, bm.config.SyncAddr)
			} else {
				bm.syncService = NewSyncService(&bm.config.Sync)
			}
			if err := bm.syncService.Start(bm.ctx); err != nil {
				return fmt.Errorf("启动同步发送服务失败: %w", err)
			}
		}

		// 启动会话同步
		go func() {
			time.Sleep(2 * time.Second)
			bm.startSessionSyncHooks()
		}()

	} else {
		// 切换到备份模式：启动接收，停止发送
		if bm.syncService != nil {
			if err := bm.syncService.Stop(); err != nil {
				log.Printf("停止同步发送服务失败: %v", err)
			}
			bm.syncService = nil
		}

		if bm.syncReceiver == nil {
			// 使用分离的同步地址
			if bm.config.SyncAddr != "" {
				bm.syncReceiver = newSyncReceiverWithAddr(&bm.config.Sync, bm.sessionMgr, bm.config.SyncAddr)
			} else {
				bm.syncReceiver = newSyncReceiver(&bm.config.Sync, bm.sessionMgr)
			}
			if err := bm.syncReceiver.Start(bm.ctx); err != nil {
				return fmt.Errorf("启动同步接收服务失败: %w", err)
			}
		}
	}

	log.Printf("模式切换回调执行完成")
	return nil
}

func (bm *BackupManagerImpl) onSessionRestore() error {
	log.Printf("执行会话恢复回调")

	if bm.sessionMgr == nil {
		return fmt.Errorf("会话管理器未设置")
	}

	sessionCount := bm.sessionMgr.GetSessionCount()
	log.Printf("当前会话数量: %d", sessionCount)

	if bm.syncService != nil {
		go func() {
			time.Sleep(5 * time.Second)
			if err := bm.syncService.FullSync(); err != nil {
				log.Printf("恢复后全量同步失败: %v", err)
			}
		}()
	}

	log.Printf("会话恢复回调执行完成")
	return nil
}

func (bm *BackupManagerImpl) getModeString() string {
	return bm.getModeStringForMode(bm.GetMode())
}

func (bm *BackupManagerImpl) getModeStringForMode(mode ServerMode) string {
	switch mode {
	case ModePrimary:
		return "主服务器"
	case ModeBackup:
		return "备份服务器"
	default:
		return "未知"
	}
}

func (bm *BackupManagerImpl) isStopped() bool {
	bm.stopMux.RLock()
	defer bm.stopMux.RUnlock()
	return bm.stopped
}
