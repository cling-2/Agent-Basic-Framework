package auth

import (
	"context"
	"sort"
	"sync"
	"time"

	"kingsoft-agent/pkg/model"

	"github.com/google/uuid"
)

// SessionStore 会话存储抽象接口
type SessionStore interface {
	// Create 创建新会话，若用户活跃会话数已达上限则自动淘汰最早的
	Create(ctx context.Context, session *model.Session) error

	// Get 根据 sessionID 获取会话，若已过期自动标记为 expired 并返回 ErrSessionExpired
	Get(ctx context.Context, sessionID string) (*model.Session, error)

	// Delete 删除会话（主动登出时调用）
	Delete(ctx context.Context, sessionID string) error

	// Renew 续期会话，将 expires_at 延长一个 TTL（不超过最大生命周期）
	Renew(ctx context.Context, sessionID string) error

	// Close 停止后台清理协程
	Close()
}

// MemorySessionStore 基于 sync.Map 的内存会话存储
type MemorySessionStore struct {
	sessions sync.Map // sessionID -> *model.Session
	stopCh   chan struct{}
}

// NewMemorySessionStore 创建内存会话存储实例，并启动过期清理协程
func NewMemorySessionStore() *MemorySessionStore {
	s := &MemorySessionStore{
		stopCh: make(chan struct{}),
	}
	s.startCleanup()
	return s
}

// Create 创建新会话
func (s *MemorySessionStore) Create(_ context.Context, session *model.Session) error {
	// 检查同一用户活跃会话数上限
	var userSessions []*model.Session
	s.sessions.Range(func(key, value interface{}) bool {
		sess := value.(*model.Session)
		if sess.UserID == session.UserID && sess.Status == model.SessionActive {
			userSessions = append(userSessions, sess)
		}
		return true
	})

	if len(userSessions) >= model.MaxSessionsPerUser {
		// 淘汰最早创建的会话（copy-on-write：创建副本后修改，避免数据竞争）
		sort.Slice(userSessions, func(i, j int) bool {
			return userSessions[i].CreatedAt.Before(userSessions[j].CreatedAt)
		})
		oldest := userSessions[0]
		now := timeNow()
		revoked := *oldest // 副本
		revoked.Status = model.SessionRevoked
		revoked.UpdatedAt = now
		s.sessions.Store(oldest.SessionID, &revoked)
	}

	s.sessions.Store(session.SessionID, session)
	return nil
}

// Get 获取会话（返回防御性副本，防止调用方修改内部状态）
func (s *MemorySessionStore) Get(_ context.Context, sessionID string) (*model.Session, error) {
	val, ok := s.sessions.Load(sessionID)
	if !ok {
		return nil, ErrSessionNotFound
	}

	sess := val.(*model.Session)

	// 已过期或已撤销
	if sess.Status != model.SessionActive {
		return nil, ErrSessionExpired
	}

	// 检查是否超过有效期
	now := timeNow()
	if now.After(sess.ExpiresAt) {
		// copy-on-write：创建副本后修改
		expired := *sess
		expired.Status = model.SessionExpired
		expired.UpdatedAt = now
		s.sessions.Store(sessionID, &expired)
		return nil, ErrSessionExpired
	}

	// 返回防御性副本
	copy := *sess
	return &copy, nil
}

// Delete 删除会话（主动登出，copy-on-write）
func (s *MemorySessionStore) Delete(_ context.Context, sessionID string) error {
	val, ok := s.sessions.Load(sessionID)
	if !ok {
		return ErrSessionNotFound
	}

	sess := val.(*model.Session)
	revoked := *sess // 副本
	revoked.Status = model.SessionRevoked
	revoked.UpdatedAt = timeNow()
	s.sessions.Store(sessionID, &revoked)
	return nil
}

// Renew 续期会话（copy-on-write）
func (s *MemorySessionStore) Renew(_ context.Context, sessionID string) error {
	val, ok := s.sessions.Load(sessionID)
	if !ok {
		return ErrSessionNotFound
	}

	sess := val.(*model.Session)
	if sess.Status != model.SessionActive {
		return ErrSessionExpired
	}

	now := timeNow()
	// 计算最大允许过期时间（创建时间 + 最大生命周期）
	maxExpiry := sess.CreatedAt.Add(model.MaxSessionLifetime)
	newExpiry := now.Add(model.DefaultSessionTTL)

	// 续期不可超过最大生命周期
	if newExpiry.After(maxExpiry) {
		newExpiry = maxExpiry
	}

	// 已超过最大生命周期
	if now.After(maxExpiry) {
		expired := *sess // 副本
		expired.Status = model.SessionExpired
		expired.UpdatedAt = now
		s.sessions.Store(sessionID, &expired)
		return ErrSessionExpired
	}

	// copy-on-write：创建副本后修改
	renewed := *sess
	renewed.ExpiresAt = newExpiry
	renewed.UpdatedAt = now
	s.sessions.Store(sessionID, &renewed)
	return nil
}

// Close 停止后台清理协程
func (s *MemorySessionStore) Close() {
	close(s.stopCh)
}

// startCleanup 启动后台过期清理协程
func (s *MemorySessionStore) startCleanup() {
	ticker := time.NewTicker(model.CleanupInterval)
	go func() {
		for {
			select {
			case <-ticker.C:
				s.cleanExpired()
			case <-s.stopCh:
				ticker.Stop()
				return
			}
		}
	}()
}

// cleanExpired 清理过期会话（copy-on-write）
func (s *MemorySessionStore) cleanExpired() {
	now := timeNow()
	s.sessions.Range(func(key, value interface{}) bool {
		sess := value.(*model.Session)
		if sess.Status == model.SessionActive && now.After(sess.ExpiresAt) {
			expired := *sess // 副本
			expired.Status = model.SessionExpired
			expired.UpdatedAt = now
			s.sessions.Store(key, &expired)
		}
		return true
	})
}

// NewSession 为指定用户创建新会话实体（生成 UUID v4 SessionID，设置 TTL）
func NewSession(userID int64) *model.Session {
	now := timeNow()
	return &model.Session{
		UserID:    userID,
		SessionID: uuid.New().String(),
		Status:    model.SessionActive,
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(model.DefaultSessionTTL),
	}
}
