// Package session 提供会话管理功能
package session

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/quic-go/quic-go"
)

// 会话管理器
type Manager struct {
	sessions      sync.Map // map[string]*Session 所有活跃会话（
	sessionsByUID sync.Map // map[string]*Session 按用户ID索引的会话

	// 超时配置
	sessionTimeout      time.Duration
	ackTimeout          time.Duration
	maxRetries          int
	connectionDownDelay time.Duration

	// 用于停止清理goroutine
	stopCh chan struct{}
	wg     sync.WaitGroup

	// 备份同步相关
	syncEnabled     bool
	syncCallback    func(sessionID string, session *Session, event string)
	syncCallbackMux sync.RWMutex
	backupMode      bool
}

// 创建新的会话管理器
func NewManager(sessionTimeout, ackTimeout time.Duration, maxRetries int) *Manager {
	return &Manager{
		sessionTimeout:      sessionTimeout,
		ackTimeout:          ackTimeout,
		maxRetries:          maxRetries,
		connectionDownDelay: 30 * time.Second, // 默认30秒延迟清理
		stopCh:              make(chan struct{}),
	}
}

// 创建或重连会话
func (m *Manager) CreateOrReconnectSession(conn *quic.Conn, stream *quic.Stream, clientID, openID, userIP string) (*Session, bool) {
	var oldSession *Session
	var isReconnect bool

	if openID != "" {
		if value, exists := m.sessionsByUID.Load(openID); exists {
			existingSession := value.(*Session)
			// 检查是否可以重连
			if time.Since(existingSession.LastActivity) < m.connectionDownDelay*2 {
				oldSession = existingSession
				isReconnect = true
			}
		}
	}

	// 锁外创建新会话对象
	sessionID := uuid.New().String()
	newSession := &Session{
		ID:           sessionID,
		Connection:   conn,
		Stream:       stream,
		CreateTime:   time.Now(),
		LastActivity: time.Now(),

		// 客户端信息
		ClientID: clientID,
		OpenID:   openID,
		UserIP:   userIP,
		connIdx:  1, // 正常连接

		// 初始状态
		nextSeqID: 1,
		state:     int32(SessionInited),

		closeCh: make(chan struct{}),
	}

	newSession.orderedQueue = NewOrderedMessageQueue(sessionID, 1000)
	newSession.orderingManager = NewMessageOrderingManager()

	// 原子操作处理重连和注册
	if isReconnect && oldSession != nil {
		if currentValue, exists := m.sessions.Load(oldSession.ID); exists {
			if currentOldSession := currentValue.(*Session); currentOldSession == oldSession {
				// 重连处理
				newSession.InheritFrom(oldSession)
				newSession.SetSuccessor(true)
				m.handleOldSessionOnReconnectAtomic(oldSession, newSession)

				fmt.Printf("检测到重连 - 客户端: %s, 用户: %s, 旧会话: %s, 新会话: %s\n",
					clientID, openID, oldSession.ID, newSession.ID)
			} else {
				isReconnect = false
			}
		} else {
			isReconnect = false
		}
	}

	m.sessions.Store(sessionID, newSession)
	if openID != "" {
		m.sessionsByUID.Store(openID, newSession)
	}

	return newSession, isReconnect
}

// 处理重连时的旧会话
func (m *Manager) handleOldSessionOnReconnectAtomic(oldSession, newSession *Session) {
	oldSession.SetClosed()

	m.sessions.Delete(oldSession.ID)

	go func() {
		time.Sleep(5 * time.Second) // 给重传一些时间
		oldSession.Close()
	}()
}

// 获取指定会话
func (m *Manager) GetSession(sessionID string) (*Session, bool) {
	if value, ok := m.sessions.Load(sessionID); ok {
		return value.(*Session), true
	}
	return nil, false
}

// 获取所有活跃会话
func (m *Manager) GetAllSessions() []*Session {
	sessions := make([]*Session, 0)
	m.sessions.Range(func(key, value interface{}) bool {
		if session := value.(*Session); session != nil {
			sessions = append(sessions, session)
		}
		return true
	})
	return sessions
}

// 移除会话
func (m *Manager) RemoveSession(sessionID string) {
	m.RemoveSessionWithDelay(sessionID, false, "immediate removal")
}

// 获取会话总数
func (m *Manager) GetSessionCount() int {
	count := 0
	m.sessions.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}

// 启动会话管理器
func (m *Manager) Start(ctx context.Context) {
	m.wg.Add(1)
	go m.cleanupExpiredSessions(ctx)
}

func (m *Manager) Stop() {
	close(m.stopCh)
	m.wg.Wait()

	m.sessions.Range(func(key, value interface{}) bool {
		if session := value.(*Session); session != nil {
			session.Close()
		}
		return true
	})
	m.sessions.Range(func(key, value interface{}) bool {
		m.sessions.Delete(key)
		return true
	})
	m.sessionsByUID.Range(func(key, value interface{}) bool {
		m.sessionsByUID.Delete(key)
		return true
	})
}

func (m *Manager) cleanupExpiredSessions(ctx context.Context) {
	defer m.wg.Done()

	ticker := time.NewTicker(30 * time.Second) // 每30秒清理一次
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.removeExpiredSessions()
		}
	}
}

func (m *Manager) removeExpiredSessions() {
	expiredSessions := make([]string, 0)

	// 遍历找出过期会话
	m.sessions.Range(func(key, value interface{}) bool {
		sessionID := key.(string)
		session := value.(*Session)
		if session.IsExpired(m.sessionTimeout) {
			expiredSessions = append(expiredSessions, sessionID)
		}
		return true
	})

	// 移除过期会话
	for _, sessionID := range expiredSessions {
		if value, exists := m.sessions.Load(sessionID); exists {
			session := value.(*Session)
			m.sessions.Delete(sessionID)
			session.Close()
			fmt.Printf("会话 %s 已过期并被移除\n", sessionID)
		}
	}
}

// 批量ACK
func (m *Manager) AckMessagesUpTo(sessionID string, ackSeqID uint64) int {
	session, exists := m.GetSession(sessionID)
	if !exists {
		return 0
	}

	session.UpdateAckServerSeq(ackSeqID)

	orderedQueue := session.GetOrderedQueue()
	if orderedQueue == nil {
		return 0
	}

	return orderedQueue.AckMessagesUpTo(ackSeqID)
}

func (m *Manager) ActivateSession(sessionID string, zone int64) error {
	value, exists := m.sessions.Load(sessionID)
	if !exists {
		return fmt.Errorf("会话不存在: %s", sessionID)
	}

	session := value.(*Session)

	// 激活会话状态
	if !session.ActivateSession(zone) {
		return fmt.Errorf("会话激活失败，当前状态不是Inited: %s", sessionID)
	}

	fmt.Printf("会话激活成功 - 会话: %s, Zone: %d\n", sessionID, zone)
	return nil
}

func (m *Manager) writeMessageToSession(session *Session, data []byte) error {
	if session.IsClosed() {
		return fmt.Errorf("会话已关闭")
	}

	_, err := session.Stream.Write(data)
	return err
}

func (m *Manager) ValidateClientSequence(sessionID string, clientSeq uint64) bool {
	session, exists := m.GetSession(sessionID)
	if !exists {
		return false
	}

	return session.ValidateClientSeq(clientSeq)
}

func (m *Manager) GetExpectedClientSequence(sessionID string) uint64 {
	session, exists := m.GetSession(sessionID)
	if !exists {
		return 1 // 如果会话不存在，期待的是第一个序列号
	}

	return session.MaxClientSeq() + 1
}

func (m *Manager) GetPendingCount(sessionID string) int {
	session, exists := m.GetSession(sessionID)
	if !exists {
		return 0
	}

	orderedQueue := session.GetOrderedQueue()
	if orderedQueue == nil {
		return 0
	}

	return orderedQueue.GetPendingCount()
}

func (m *Manager) GetSessionByOpenID(openID string) (*Session, bool) {
	if value, ok := m.sessionsByUID.Load(openID); ok {
		return value.(*Session), true
	}
	return nil, false
}

func (m *Manager) BindSession(session *Session) error {

	if session.OpenID != "" {
		// 检查OpenID是否已被占用
		if existingValue, exists := m.sessionsByUID.Load(session.OpenID); exists {
			existingSession := existingValue.(*Session)
			if existingSession.ID != session.ID {
				return fmt.Errorf("OpenID %s 已被会话 %s 占用", session.OpenID, existingSession.ID)
			}
		}

		m.sessionsByUID.Store(session.OpenID, session)
	}

	return nil
}

// 延迟移除会话
func (m *Manager) RemoveSessionWithDelay(sessionID string, delay bool, reason string) {
	value, exists := m.sessions.Load(sessionID)
	if !exists {
		return
	}

	session := value.(*Session)

	// 标记会话为关闭状态
	alreadyClosed := session.SetClosed()
	if alreadyClosed && !delay {
		// 已经关闭且不延迟，立即删除
		m.immediateRemoveSession(sessionID)
		return
	}

	if delay && session.OpenID != "" {
		// 延迟清理，支持重连
		fmt.Printf("会话 %s 将在 %v 后清理，原因: %s\n", sessionID, m.connectionDownDelay, reason)

		// 不立即从索引中移除，允许重连期间查找
		time.AfterFunc(m.connectionDownDelay, func() {
			m.immediateRemoveSession(sessionID)
		})
	} else {
		m.immediateRemoveSession(sessionID)
	}
}

// 立即移除会话
func (m *Manager) immediateRemoveSession(sessionID string) {
	value, exists := m.sessions.Load(sessionID)
	if !exists {
		return
	}

	session := value.(*Session)

	// 从所有索引移除
	m.sessions.Delete(sessionID)

	if session.OpenID != "" {
		m.sessionsByUID.Delete(session.OpenID)
	}

	session.Close()
	fmt.Printf("会话 %s 已被彻底清理\n", sessionID)
}

func (m *Manager) EnableSync(callback func(sessionID string, session *Session, event string)) {
	m.syncCallbackMux.Lock()
	defer m.syncCallbackMux.Unlock()

	m.syncEnabled = true
	m.syncCallback = callback
	fmt.Printf("会话管理器同步功能已启用\n")
}
