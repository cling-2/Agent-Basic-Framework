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
		// 淘汰最早创建的会话
		sort.Slice(userSessions, func(i, j int) bool {
			return userSessions[i].CreatedAt.Before(userSessions[j].CreatedAt)
		})
		oldest := userSessions[0]
		now := timeNow()
		oldest.Status = model.SessionRevoked
		oldest.UpdatedAt = now
		s.sessions.Store(oldest.SessionID, oldest)
	}

	s.sessions.Store(session.SessionID, session)
	return nil
}

// Get 获取会话
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
		sess.Status = model.SessionExpired
		sess.UpdatedAt = now
		s.sessions.Store(sessionID, sess)
		return nil, ErrSessionExpired
	}

	return sess, nil
}

// Delete 删除会话（主动登出）
func (s *MemorySessionStore) Delete(_ context.Context, sessionID string) error {
	val, ok := s.sessions.Load(sessionID)
	if !ok {
		return ErrSessionNotFound
	}

	sess := val.(*model.Session)
	sess.Status = model.SessionRevoked
	sess.UpdatedAt = timeNow()
	s.sessions.Store(sessionID, sess)
	return nil
}

// Renew 续期会话
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
		sess.Status = model.SessionExpired
		sess.UpdatedAt = now
		s.sessions.Store(sessionID, sess)
		return ErrSessionExpired
	}

	sess.ExpiresAt = newExpiry
	sess.UpdatedAt = now
	s.sessions.Store(sessionID, sess)
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

// cleanExpired 清理过期会话
func (s *MemorySessionStore) cleanExpired() {
	now := timeNow()
	s.sessions.Range(func(key, value interface{}) bool {
		sess := value.(*model.Session)
		if sess.Status == model.SessionActive && now.After(sess.ExpiresAt) {
			sess.Status = model.SessionExpired
			sess.UpdatedAt = now
			s.sessions.Store(key, sess)
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
