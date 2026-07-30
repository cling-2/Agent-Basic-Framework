package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"kingsoft-agent/pkg/model"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
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

// ========== MemorySessionStore 内存实现 ==========

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
		sort.Slice(userSessions, func(i, j int) bool {
			return userSessions[i].CreatedAt.Before(userSessions[j].CreatedAt)
		})
		oldest := userSessions[0]
		now := timeNow()
		revoked := *oldest
		revoked.Status = model.SessionRevoked
		revoked.UpdatedAt = now
		s.sessions.Store(oldest.SessionID, &revoked)
	}

	s.sessions.Store(session.SessionID, session)
	return nil
}

// Get 获取会话（返回防御性副本）
func (s *MemorySessionStore) Get(_ context.Context, sessionID string) (*model.Session, error) {
	val, ok := s.sessions.Load(sessionID)
	if !ok {
		return nil, ErrSessionNotFound
	}

	sess := val.(*model.Session)

	if sess.Status != model.SessionActive {
		return nil, ErrSessionExpired
	}

	now := timeNow()
	if now.After(sess.ExpiresAt) {
		expired := *sess
		expired.Status = model.SessionExpired
		expired.UpdatedAt = now
		s.sessions.Store(sessionID, &expired)
		return nil, ErrSessionExpired
	}

	copy := *sess
	return &copy, nil
}

// Delete 删除会话（主动登出）
func (s *MemorySessionStore) Delete(_ context.Context, sessionID string) error {
	val, ok := s.sessions.Load(sessionID)
	if !ok {
		return ErrSessionNotFound
	}

	sess := val.(*model.Session)
	revoked := *sess
	revoked.Status = model.SessionRevoked
	revoked.UpdatedAt = timeNow()
	s.sessions.Store(sessionID, &revoked)
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
	maxExpiry := sess.CreatedAt.Add(model.MaxSessionLifetime)
	newExpiry := now.Add(model.DefaultSessionTTL)

	if newExpiry.After(maxExpiry) {
		newExpiry = maxExpiry
	}

	if now.After(maxExpiry) {
		expired := *sess
		expired.Status = model.SessionExpired
		expired.UpdatedAt = now
		s.sessions.Store(sessionID, &expired)
		return ErrSessionExpired
	}

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

// cleanExpired 清理过期会话
func (s *MemorySessionStore) cleanExpired() {
	now := timeNow()
	s.sessions.Range(func(key, value interface{}) bool {
		sess := value.(*model.Session)
		if sess.Status == model.SessionActive && now.After(sess.ExpiresAt) {
			expired := *sess
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

// ========== RedisSessionStore 持久化实现 ==========

const (
	// Redis key 前缀
	sessionKeyPrefix = "session:"
	userSessionsKey  = "user_sessions:" // Sorted Set: userID → sessionIDs (按创建时间排序)
)

// RedisSessionStore 基于 Redis 的持久化会话存储
// Redis 优势：
//   - TTL 自动过期：每个会话设置 Redis EXPIRE，无需后台清理协程
//   - Sorted Set 管理用户会话：按创建时间排序，轻松实现淘汰最旧会话
//   - 天然支持分布式：多实例共享会话状态
type RedisSessionStore struct {
	client *redis.Client
}

// NewRedisSessionStore 创建 Redis 持久化会话存储
func NewRedisSessionStore(client *redis.Client) *RedisSessionStore {
	return &RedisSessionStore{client: client}
}

// sessionKey 生成会话的 Redis Key
func sessionKey(sessionID string) string {
	return sessionKeyPrefix + sessionID
}

// userSessionsSetKey 生成用户会话集合的 Redis Key
func userSessionsSetKey(userID int64) string {
	return fmt.Sprintf("%s%d", userSessionsKey, userID)
}

// Create 创建新会话
func (s *RedisSessionStore) Create(ctx context.Context, session *model.Session) error {
	// 1. 检查同一用户活跃会话数上限，淘汰最旧会话
	userKey := userSessionsSetKey(session.UserID)
	// 移除已过期/已撤销的会话ID（清理脏数据）
	s.cleanUserSessions(ctx, session.UserID)

	// 获取当前用户的活跃会话数
	count, err := s.client.ZCard(ctx, userKey).Result()
	if err != nil {
		return fmt.Errorf("查询用户会话数失败: %w", err)
	}

	if count >= int64(model.MaxSessionsPerUser) {
		// 淘汰最早创建的会话：取出分数最小的（创建时间最早的）
		oldestIDs, err := s.client.ZRange(ctx, userKey, 0, 0).Result()
		if err != nil {
			return fmt.Errorf("查询最旧会话失败: %w", err)
		}
		for _, id := range oldestIDs {
			// 标记为已撤销
			s.revokeSession(ctx, id)
			s.client.ZRem(ctx, userKey, id)
		}
	}

	// 2. 存储会话数据（Hash）
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("序列化会话失败: %w", err)
	}

	// 设置 TTL = 最大生命周期（从创建时间算起）
	ttl := model.MaxSessionLifetime
	if err := s.client.Set(ctx, sessionKey(session.SessionID), data, ttl).Err(); err != nil {
		return fmt.Errorf("存储会话失败: %w", err)
	}

	// 3. 添加到用户会话 Sorted Set（分数 = 创建时间戳）
	if err := s.client.ZAdd(ctx, userKey, redis.Z{
		Score:  float64(session.CreatedAt.Unix()),
		Member: session.SessionID,
	}).Err(); err != nil {
		return fmt.Errorf("添加用户会话索引失败: %w", err)
	}

	return nil
}

// Get 获取会话
func (s *RedisSessionStore) Get(ctx context.Context, sessionID string) (*model.Session, error) {
	data, err := s.client.Get(ctx, sessionKey(sessionID)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("查询会话失败: %w", err)
	}

	var sess model.Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("解析会话数据失败: %w", err)
	}

	// 检查状态
	if sess.Status != model.SessionActive {
		return nil, ErrSessionExpired
	}

	// 检查过期
	now := timeNow()
	if now.After(sess.ExpiresAt) {
		s.revokeSession(ctx, sessionID)
		return nil, ErrSessionExpired
	}

	return &sess, nil
}

// Delete 删除会话（主动登出）
func (s *RedisSessionStore) Delete(ctx context.Context, sessionID string) error {
	// 获取会话以确定 userID（用于从用户会话集合中移除）
	sess, err := s.Get(ctx, sessionID)
	if err != nil {
		return err
	}

	// 标记为已撤销
	s.revokeSession(ctx, sessionID)

	// 从用户会话集合中移除
	s.client.ZRem(ctx, userSessionsSetKey(sess.UserID), sessionID)

	return nil
}

// Renew 续期会话
func (s *RedisSessionStore) Renew(ctx context.Context, sessionID string) error {
	data, err := s.client.Get(ctx, sessionKey(sessionID)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return ErrSessionNotFound
		}
		return fmt.Errorf("查询会话失败: %w", err)
	}

	var sess model.Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return fmt.Errorf("解析会话数据失败: %w", err)
	}

	if sess.Status != model.SessionActive {
		return ErrSessionExpired
	}

	now := timeNow()
	maxExpiry := sess.CreatedAt.Add(model.MaxSessionLifetime)
	newExpiry := now.Add(model.DefaultSessionTTL)

	if newExpiry.After(maxExpiry) {
		newExpiry = maxExpiry
	}

	if now.After(maxExpiry) {
		s.revokeSession(ctx, sessionID)
		return ErrSessionExpired
	}

	// 更新会话数据
	sess.ExpiresAt = newExpiry
	sess.UpdatedAt = now
	updatedData, err := json.Marshal(&sess)
	if err != nil {
		return fmt.Errorf("序列化会话失败: %w", err)
	}

	// 更新 Redis TTL = 剩余最大生命周期
	remainingTTL := time.Until(maxExpiry)
	if remainingTTL < 0 {
		remainingTTL = time.Minute // 至少保留1分钟，避免立即过期
	}
	if err := s.client.Set(ctx, sessionKey(sessionID), updatedData, remainingTTL).Err(); err != nil {
		return fmt.Errorf("更新会话失败: %w", err)
	}

	return nil
}

// Close 实现 SessionStore 接口（Redis 无需后台协程，TTL 自动过期）
func (s *RedisSessionStore) Close() {
	// 无操作：Redis TTL 自动处理过期，无需后台清理协程
}

// revokeSession 标记会话为已撤销并设置短 TTL 后删除
func (s *RedisSessionStore) revokeSession(ctx context.Context, sessionID string) {
	data, err := s.client.Get(ctx, sessionKey(sessionID)).Bytes()
	if err != nil {
		return // 会话已不存在，无需处理
	}

	var sess model.Session
	if err := json.Unmarshal(data, &sess); err != nil {
		s.client.Del(ctx, sessionKey(sessionID)) // 数据损坏，直接删除
		return
	}

	sess.Status = model.SessionRevoked
	sess.UpdatedAt = timeNow()
	updatedData, _ := json.Marshal(&sess)

	// 已撤销会话保留5分钟后自动删除（给查询一个缓冲期）
	s.client.Set(ctx, sessionKey(sessionID), updatedData, 5*time.Minute)
}

// cleanUserSessions 清理用户会话集合中已过期的会话ID
func (s *RedisSessionStore) cleanUserSessions(ctx context.Context, userID int64) {
	userKey := userSessionsSetKey(userID)
	// 获取所有会话ID
	ids, err := s.client.ZRange(ctx, userKey, 0, -1).Result()
	if err != nil {
		return
	}
	// 检查每个会话是否仍存在
	for _, id := range ids {
		exists, err := s.client.Exists(ctx, sessionKey(id)).Result()
		if err == nil && exists == 0 {
			// 会话已过期被 Redis 自动删除，从集合中移除
			s.client.ZRem(ctx, userKey, id)
		}
	}
}
