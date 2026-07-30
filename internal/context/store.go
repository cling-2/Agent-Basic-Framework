package context

import (
	stdctx "context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/cloudwego/eino/schema"
	"github.com/redis/go-redis/v9"
)

// MessageStore 消息历史存储接口
// 始终存储完整原始历史，裁剪版 Prompt 仅用于本次 LLM 调用，不回写 Store
type MessageStore interface {
	// Get 获取指定线程的消息历史（返回防御性副本）
	// 未知 threadID 返回 nil, nil（空历史不是错误）
	Get(ctx stdctx.Context, threadID string) ([]*schema.Message, error)

	// Append 追加消息到指定线程（存储完整原始消息）
	Append(ctx stdctx.Context, threadID string, msg *schema.Message) error

	// Clear 清除指定线程的消息历史
	Clear(ctx stdctx.Context, threadID string) error

	// SetOwner 设置线程所属用户（首次写入时调用，用于数据隔离校验）
	SetOwner(ctx stdctx.Context, threadID string, userID int64) error

	// GetOwner 获取线程所属用户 ID，未设置返回 (0, false)
	GetOwner(ctx stdctx.Context, threadID string) (int64, bool)
}

// ========== MemoryMessageStore 内存实现 ==========

// MemoryMessageStore 内存版消息历史存储
type MemoryMessageStore struct {
	mu       sync.RWMutex
	messages map[string][]*schema.Message // threadID → 消息列表
	owners   map[string]int64             // threadID → userID（数据隔离）
}

// NewMemoryMessageStore 创建内存版消息历史存储
func NewMemoryMessageStore() *MemoryMessageStore {
	return &MemoryMessageStore{
		messages: make(map[string][]*schema.Message),
		owners:   make(map[string]int64),
	}
}

func (s *MemoryMessageStore) Get(ctx stdctx.Context, threadID string) ([]*schema.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msgs, ok := s.messages[threadID]
	if !ok {
		return nil, nil
	}
	result := make([]*schema.Message, len(msgs))
	copy(result, msgs)
	return result, nil
}

func (s *MemoryMessageStore) Append(ctx stdctx.Context, threadID string, msg *schema.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages[threadID] = append(s.messages[threadID], msg)
	return nil
}

func (s *MemoryMessageStore) Clear(ctx stdctx.Context, threadID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.messages, threadID)
	delete(s.owners, threadID)
	return nil
}

func (s *MemoryMessageStore) SetOwner(_ stdctx.Context, threadID string, userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.owners[threadID] = userID
	return nil
}

func (s *MemoryMessageStore) GetOwner(_ stdctx.Context, threadID string) (int64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.owners[threadID]
	return id, ok
}

// ========== RedisMessageStore 持久化实现 ==========

const (
	msgKeyPrefix   = "msg:"       // Hash: threadID → 消息列表（JSON 序列化）
	msgOwnerPrefix = "msg_owner:" // String: threadID → userID
)

// RedisMessageStore 基于 Redis 的持久化消息历史存储
// Redis 优势：
//   - List 天然支持追加（RPUSH）和范围读取（LRANGE）
//   - 持久化（AOF/RDB），重启不丢失
//   - 支持分布式部署
type RedisMessageStore struct {
	client *redis.Client
}

// NewRedisMessageStore 创建 Redis 持久化消息历史存储
func NewRedisMessageStore(client *redis.Client) *RedisMessageStore {
	return &RedisMessageStore{client: client}
}

// msgListKey 生成消息列表的 Redis Key
func msgListKey(threadID string) string {
	return msgKeyPrefix + threadID
}

// msgOwnerKey 生成消息所有者的 Redis Key
func msgOwnerKey(threadID string) string {
	return msgOwnerPrefix + threadID
}

// Get 获取指定线程的消息历史
func (s *RedisMessageStore) Get(ctx stdctx.Context, threadID string) ([]*schema.Message, error) {
	// 从 Redis List 读取所有消息（每条是一个 JSON 字符串）
	results, err := s.client.LRange(ctx, msgListKey(threadID), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("查询消息历史失败: %w", err)
	}

	if len(results) == 0 {
		return nil, nil // 空历史，不是错误
	}

	// 反序列化每条消息
	msgs := make([]*schema.Message, 0, len(results))
	for _, data := range results {
		var msg schema.Message
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			// 单条消息损坏不中断，跳过并记录
			continue
		}
		msgs = append(msgs, &msg)
	}

	return msgs, nil
}

// Append 追加消息到指定线程
func (s *RedisMessageStore) Append(ctx stdctx.Context, threadID string, msg *schema.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("序列化消息失败: %w", err)
	}

	// RPUSH 追加到 List 尾部
	if err := s.client.RPush(ctx, msgListKey(threadID), data).Err(); err != nil {
		return fmt.Errorf("追加消息失败: %w", err)
	}

	return nil
}

// Clear 清除指定线程的消息历史
func (s *RedisMessageStore) Clear(ctx stdctx.Context, threadID string) error {
	// 删除消息列表和所有者信息
	s.client.Del(ctx, msgListKey(threadID))
	s.client.Del(ctx, msgOwnerKey(threadID))
	return nil
}

// SetOwner 设置线程所属用户
func (s *RedisMessageStore) SetOwner(ctx stdctx.Context, threadID string, userID int64) error {
	if err := s.client.Set(ctx, msgOwnerKey(threadID), strconv.FormatInt(userID, 10), 0).Err(); err != nil {
		return fmt.Errorf("设置线程所有者失败: %w", err)
	}
	return nil
}

// GetOwner 获取线程所属用户 ID
func (s *RedisMessageStore) GetOwner(ctx stdctx.Context, threadID string) (int64, bool) {
	val, err := s.client.Get(ctx, msgOwnerKey(threadID)).Result()
	if err != nil {
		return 0, false
	}
	userID, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, false
	}
	return userID, true
}
