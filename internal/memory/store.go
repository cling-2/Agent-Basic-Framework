package memory

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ========== 数据模型 ==========

// MemoryEntry 长期记忆条目
// 以 key-value 形式存储用户画像、偏好和事实
// 按 userId 隔离，同一用户下 key 唯一
type MemoryEntry struct {
	ID        int64     `json:"id"`         // 条目 ID（自增）
	UserID    int64     `json:"user_id"`    // 归属用户 ID（命名空间）
	Key       string    `json:"key"`        // 条目键（如 "preference_language"）
	Value     string    `json:"value"`      // 条目值（如 "Python"）
	Category  string    `json:"category"`   // 分类（preference / fact / rule）
	CreatedAt time.Time `json:"created_at"` // 创建时间
	UpdatedAt time.Time `json:"updated_at"` // 最后更新时间
}

// ========== MemoryStore 接口 ==========

// MemoryStore 长期记忆存储接口
// 按 userId 隔离，存储用户画像、偏好和事实
// 内存版与持久化版可平滑切换，业务代码无需改动
type MemoryStore interface {
	// Put 写入或更新一条长期记忆
	// 同一 userId + key 下，新值覆盖旧值
	Put(ctx context.Context, userID int64, entry *MemoryEntry) error

	// Get 获取指定用户的指定 key 的长期记忆
	// 不存在返回 nil, nil（空记忆不是错误）
	Get(ctx context.Context, userID int64, key string) (*MemoryEntry, error)

	// List 列出指定用户的所有长期记忆条目
	// 按 category 过滤（空字符串表示不过滤）
	List(ctx context.Context, userID int64, category string) ([]*MemoryEntry, error)

	// Delete 删除指定用户的指定 key 的长期记忆
	Delete(ctx context.Context, userID int64, key string) error
}

// ========== InMemoryMemoryStore 实现 ==========

// InMemoryMemoryStore 内存版长期记忆存储
// 基于 sync.RWMutex + map，进程内存储，重启丢失
// 无需任何外部依赖即可运行
type InMemoryMemoryStore struct {
	mu      sync.RWMutex
	entries map[string]*MemoryEntry // key: "{userID}:{entryKey}" → MemoryEntry
	nextID  int64                   // 自增 ID 生成器
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

// Put 写入或更新一条长期记忆
// 同一 userId + key 下，新值覆盖旧值
func (s *InMemoryMemoryStore) Put(_ context.Context, userID int64, entry *MemoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sk := storageKey(userID, entry.Key)
	now := time.Now()

	if existing, ok := s.entries[sk]; ok {
		// 覆盖更新
		existing.Value = entry.Value
		existing.Category = entry.Category
		existing.UpdatedAt = now
	} else {
		// 新增
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

// Get 获取指定用户的指定 key 的长期记忆
// 不存在返回 nil, nil（空记忆不是错误）
func (s *InMemoryMemoryStore) Get(_ context.Context, userID int64, key string) (*MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sk := storageKey(userID, key)
	entry, ok := s.entries[sk]
	if !ok {
		return nil, nil
	}
	// 返回副本
	cp := *entry
	return &cp, nil
}

// List 列出指定用户的所有长期记忆条目
// 按 category 过滤（空字符串表示不过滤）
func (s *InMemoryMemoryStore) List(_ context.Context, userID int64, category string) ([]*MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prefix := fmt.Sprintf("%d:", userID)
	var result []*MemoryEntry

	for sk, entry := range s.entries {
		// 过滤：前缀匹配 userId
		if len(sk) < len(prefix) || sk[:len(prefix)] != prefix {
			continue
		}
		// 过滤：category 匹配
		if category != "" && entry.Category != category {
			continue
		}
		// 返回副本
		cp := *entry
		result = append(result, &cp)
	}

	return result, nil
}

// Delete 删除指定用户的指定 key 的长期记忆
func (s *InMemoryMemoryStore) Delete(_ context.Context, userID int64, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sk := storageKey(userID, key)
	delete(s.entries, sk)
	return nil
}
