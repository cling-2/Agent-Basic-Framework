package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// ========== 数据模型 ==========

// MemoryEntry 长期记忆条目
type MemoryEntry struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	Category  string    `json:"category"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ========== MemoryStore 接口 ==========

// MemoryStore 长期记忆存储接口
type MemoryStore interface {
	Put(ctx context.Context, userID int64, entry *MemoryEntry) error
	Get(ctx context.Context, userID int64, key string) (*MemoryEntry, error)
	List(ctx context.Context, userID int64, category string) ([]*MemoryEntry, error)
	Delete(ctx context.Context, userID int64, key string) error
}

// ========== InMemoryMemoryStore 内存实现 ==========

// InMemoryMemoryStore 内存版长期记忆存储
type InMemoryMemoryStore struct {
	mu      sync.RWMutex
	entries map[string]*MemoryEntry // key: "{userID}:{entryKey}" → MemoryEntry
	nextID  int64
}

// NewInMemoryMemoryStore 创建内存版长期记忆存储
func NewInMemoryMemoryStore() *InMemoryMemoryStore {
	return &InMemoryMemoryStore{
		entries: make(map[string]*MemoryEntry),
		nextID:  1,
	}
}

// storageKey 生成存储键："{userID}:{entryKey}"
func storageKey(userID int64, key string) string {
	return fmt.Sprintf("%d:%s", userID, key)
}

func (s *InMemoryMemoryStore) Put(_ context.Context, userID int64, entry *MemoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sk := storageKey(userID, entry.Key)
	now := time.Now()

	if existing, ok := s.entries[sk]; ok {
		existing.Value = entry.Value
		existing.Category = entry.Category
		existing.UpdatedAt = now
	} else {
		s.entries[sk] = &MemoryEntry{
			ID:        s.nextID,
			UserID:    userID,
			Key:       entry.Key,
			Value:     entry.Value,
			Category:  entry.Category,
			CreatedAt: now,
			UpdatedAt: now,
		}
		s.nextID++
	}

	return nil
}

func (s *InMemoryMemoryStore) Get(_ context.Context, userID int64, key string) (*MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sk := storageKey(userID, key)
	entry, ok := s.entries[sk]
	if !ok {
		return nil, nil
	}
	cp := *entry
	return &cp, nil
}

func (s *InMemoryMemoryStore) List(_ context.Context, userID int64, category string) ([]*MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prefix := fmt.Sprintf("%d:", userID)
	var result []*MemoryEntry

	for sk, entry := range s.entries {
		if len(sk) < len(prefix) || sk[:len(prefix)] != prefix {
			continue
		}
		if category != "" && entry.Category != category {
			continue
		}
		cp := *entry
		result = append(result, &cp)
	}

	return result, nil
}

func (s *InMemoryMemoryStore) Delete(_ context.Context, userID int64, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sk := storageKey(userID, key)
	delete(s.entries, sk)
	return nil
}

// ========== RedisMemoryStore 持久化实现 ==========

const (
	memoryKeyPrefix = "memory:"       // Hash: userID → {entryKey: JSON}
	memoryIDKey     = "memory:next_id" // String: 自增 ID 计数器
)

// RedisMemoryStore 基于 Redis 的持久化长期记忆存储
// Redis 优势：
//   - Hash 结构天然支持按 userID + entryKey 的 upsert 操作
//   - 持久化（AOF/RDB），重启不丢失
//   - 支持分布式部署
type RedisMemoryStore struct {
	client *redis.Client
}

// NewRedisMemoryStore 创建 Redis 持久化长期记忆存储
func NewRedisMemoryStore(client *redis.Client) *RedisMemoryStore {
	return &RedisMemoryStore{client: client}
}

// memoryHashKey 生成用户记忆 Hash 的 Redis Key
func memoryHashKey(userID int64) string {
	return fmt.Sprintf("%s%d", memoryKeyPrefix, userID)
}

// Put 写入或更新一条长期记忆
func (s *RedisMemoryStore) Put(ctx context.Context, userID int64, entry *MemoryEntry) error {
	hashKey := memoryHashKey(userID)
	now := time.Now()

	// 检查是否已存在
	existingData, err := s.client.HGet(ctx, hashKey, entry.Key).Result()
	var newEntry MemoryEntry
	if err == nil && existingData != "" {
		// 已存在，覆盖更新
		if err := json.Unmarshal([]byte(existingData), &newEntry); err != nil {
			// 数据损坏，重新创建
			newEntry.ID, _ = s.client.Incr(ctx, memoryIDKey).Result()
		}
		newEntry.Value = entry.Value
		newEntry.Category = entry.Category
		newEntry.UpdatedAt = now
	} else {
		// 新增
		newEntry = MemoryEntry{
			ID:        0, // 会在下面设置
			UserID:    userID,
			Key:       entry.Key,
			Value:     entry.Value,
			Category:  entry.Category,
			CreatedAt: now,
			UpdatedAt: now,
		}
		id, err := s.client.Incr(ctx, memoryIDKey).Result()
		if err != nil {
			return fmt.Errorf("生成记忆ID失败: %w", err)
		}
		newEntry.ID = id
	}

	data, err := json.Marshal(&newEntry)
	if err != nil {
		return fmt.Errorf("序列化记忆条目失败: %w", err)
	}

	// HSET: Hash 中以 entryKey 为 field
	if err := s.client.HSet(ctx, hashKey, entry.Key, data).Err(); err != nil {
		return fmt.Errorf("存储记忆条目失败: %w", err)
	}

	return nil
}

// Get 获取指定用户的指定 key 的长期记忆
func (s *RedisMemoryStore) Get(ctx context.Context, userID int64, key string) (*MemoryEntry, error) {
	hashKey := memoryHashKey(userID)
	data, err := s.client.HGet(ctx, hashKey, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // 不存在
		}
		return nil, fmt.Errorf("查询记忆条目失败: %w", err)
	}

	var entry MemoryEntry
	if err := json.Unmarshal([]byte(data), &entry); err != nil {
		return nil, fmt.Errorf("解析记忆条目失败: %w", err)
	}

	return &entry, nil
}

// List 列出指定用户的所有长期记忆条目
func (s *RedisMemoryStore) List(ctx context.Context, userID int64, category string) ([]*MemoryEntry, error) {
	hashKey := memoryHashKey(userID)

	// 获取 Hash 中所有 field-value
	result, err := s.client.HGetAll(ctx, hashKey).Result()
	if err != nil {
		return nil, fmt.Errorf("查询记忆列表失败: %w", err)
	}

	if len(result) == 0 {
		return nil, nil
	}

	var entries []*MemoryEntry
	for _, data := range result {
		var entry MemoryEntry
		if err := json.Unmarshal([]byte(data), &entry); err != nil {
			continue // 跳过损坏的条目
		}
		// 按 category 过滤
		if category != "" && entry.Category != category {
			continue
		}
		entries = append(entries, &entry)
	}

	return entries, nil
}

// Delete 删除指定用户的指定 key 的长期记忆
func (s *RedisMemoryStore) Delete(ctx context.Context, userID int64, key string) error {
	hashKey := memoryHashKey(userID)
	if err := s.client.HDel(ctx, hashKey, key).Err(); err != nil {
		return fmt.Errorf("删除记忆条目失败: %w", err)
	}
	return nil
}
