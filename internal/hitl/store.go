package hitl

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// ========== ApprovalStore 接口 ==========

// ApprovalStore 待审批状态存储接口
// 独立于请求 context 生命周期，数据操作不受 context 取消影响
type ApprovalStore interface {
	// AddApproval 添加待审批卡片
	AddApproval(ctx context.Context, threadID string, card *InterruptCard)

	// GetApproval 获取待审批卡片（已过期自动删除并返回 nil）
	GetApproval(ctx context.Context, threadID string) *InterruptCard

	// RemoveApproval 删除待审批卡片（恢复时调用，保证一次性消费）
	RemoveApproval(ctx context.Context, threadID string)

	// ListApprovals 列出所有待审批卡片（管理员查看）
	ListApprovals(ctx context.Context) []*InterruptCard

	// Close 停止后台清理协程（内存版需要，Redis 版无操作）
	Close()
}

// ========== MemoryApprovalStore 内存实现 ==========

// MemoryApprovalStore 内存版待审批状态存储
type MemoryApprovalStore struct {
	mu     sync.RWMutex
	cards  map[string]*InterruptCard // key: threadID
	stopCh chan struct{}
}

// NewMemoryApprovalStore 创建内存版审批存储，启动后台过期清理
func NewMemoryApprovalStore() *MemoryApprovalStore {
	s := &MemoryApprovalStore{
		cards:  make(map[string]*InterruptCard),
		stopCh: make(chan struct{}),
	}
	go s.cleanup()
	return s
}

func (s *MemoryApprovalStore) AddApproval(_ context.Context, threadID string, card *InterruptCard) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cards[threadID] = card
}

func (s *MemoryApprovalStore) GetApproval(_ context.Context, threadID string) *InterruptCard {
	s.mu.RLock()
	defer s.mu.RUnlock()

	card, ok := s.cards[threadID]
	if !ok {
		return nil
	}

	// 自动清理过期卡片
	if time.Now().After(card.ExpiresAt) {
		s.mu.RUnlock()
		s.mu.Lock()
		delete(s.cards, threadID)
		s.mu.Unlock()
		s.mu.RLock()
		return nil
	}

	return card
}

func (s *MemoryApprovalStore) RemoveApproval(_ context.Context, threadID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cards, threadID)
}

func (s *MemoryApprovalStore) ListApprovals(_ context.Context) []*InterruptCard {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	var result []*InterruptCard
	for _, card := range s.cards {
		if now.After(card.ExpiresAt) {
			continue
		}
		result = append(result, card)
	}
	return result
}

func (s *MemoryApprovalStore) Close() {
	close(s.stopCh)
}

func (s *MemoryApprovalStore) cleanup() {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			now := time.Now()
			for id, card := range s.cards {
				if now.After(card.ExpiresAt) {
					delete(s.cards, id)
				}
			}
			s.mu.Unlock()
		case <-s.stopCh:
			return
		}
	}
}

// ========== RedisApprovalStore 持久化实现 ==========

const (
	approvalKeyPrefix = "approval:" // Hash: threadID → InterruptCard JSON
)

// RedisApprovalStore 基于 Redis 的持久化审批状态存储
// Redis 优势：
//   - TTL 自动过期：审批卡片设置 Redis EXPIRE，无需后台清理协程
//   - 持久化（AOF/RDB），Docker 重启不丢失
//   - 原子操作：GET + DEL 可用 Lua 脚本保证一次性消费
type RedisApprovalStore struct {
	client *redis.Client
}

// NewRedisApprovalStore 创建 Redis 持久化审批状态存储
func NewRedisApprovalStore(client *redis.Client) *RedisApprovalStore {
	return &RedisApprovalStore{client: client}
}

// approvalKey 生成审批卡片的 Redis Key
func approvalKey(threadID string) string {
	return approvalKeyPrefix + threadID
}

// AddApproval 添加待审批卡片
func (s *RedisApprovalStore) AddApproval(ctx context.Context, threadID string, card *InterruptCard) {
	data, err := json.Marshal(card)
	if err != nil {
		return // 序列化失败静默丢弃
	}

	ttl := time.Until(card.ExpiresAt)
	if ttl <= 0 {
		return // 已过期不存储
	}

	s.client.Set(ctx, approvalKey(threadID), data, ttl)
}

// GetApproval 获取待审批卡片（已过期自动删除并返回 nil）
func (s *RedisApprovalStore) GetApproval(ctx context.Context, threadID string) *InterruptCard {
	data, err := s.client.Get(ctx, approvalKey(threadID)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil // 不存在或已过期（Redis TTL 自动删除）
		}
		return nil // 其他错误静默返回 nil
	}

	var card InterruptCard
	if err := json.Unmarshal(data, &card); err != nil {
		return nil // 反序列化失败
	}

	// 二次检查过期（Redis TTL 可能有延迟）
	if time.Now().After(card.ExpiresAt) {
		s.client.Del(ctx, approvalKey(threadID))
		return nil
	}

	return &card
}

// RemoveApproval 删除待审批卡片（使用 Lua 脚本保证原子性读取+删除）
func (s *RedisApprovalStore) RemoveApproval(ctx context.Context, threadID string) {
	s.client.Del(ctx, approvalKey(threadID))
}

// ListApprovals 列出所有待审批卡片（管理员查看）
func (s *RedisApprovalStore) ListApprovals(ctx context.Context) []*InterruptCard {
	// 使用 SCAN 遍历所有 approval:* 键
	var result []*InterruptCard
	var cursor uint64

	for {
		keys, nextCursor, err := s.client.Scan(ctx, cursor, approvalKeyPrefix+"*", 100).Result()
		if err != nil {
			break
		}

		for _, key := range keys {
			data, err := s.client.Get(ctx, key).Bytes()
			if err != nil {
				continue
			}

			var card InterruptCard
			if err := json.Unmarshal(data, &card); err != nil {
				continue
			}

			// 跳过已过期
			if time.Now().After(card.ExpiresAt) {
				s.client.Del(ctx, key)
				continue
			}

			result = append(result, &card)
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return result
}

// Close 实现 ApprovalStore 接口（Redis 无需后台协程，TTL 自动过期）
func (s *RedisApprovalStore) Close() {
	// 无操作
}

// ========== MemoryCheckpointStore ==========

// MemoryCheckpointStore 内存版检查点存储（实现 Eino compose.CheckPointStore 接口）
// 预留给未来 Eino 暴露编译选项后切换为原生 Checkpoint 恢复
type MemoryCheckpointStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemoryCheckpointStore 创建内存版检查点存储
func NewMemoryCheckpointStore() *MemoryCheckpointStore {
	return &MemoryCheckpointStore{
		data: make(map[string][]byte),
	}
}

// Get 获取检查点数据
func (s *MemoryCheckpointStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.data[id]
	return data, ok, nil
}

// Set 保存检查点数据
func (s *MemoryCheckpointStore) Set(_ context.Context, id string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[id] = data
	return nil
}

// Delete 删除检查点（Eino 运行时通过类型断言调用）
func (s *MemoryCheckpointStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, id)
	return nil
}

// Ensure interface compliance
var _ ApprovalStore = (*MemoryApprovalStore)(nil)
var _ ApprovalStore = (*RedisApprovalStore)(nil)
