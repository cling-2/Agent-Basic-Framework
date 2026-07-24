package context

import (
	stdctx "context"
	"sync"

	"github.com/cloudwego/eino/schema"
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
}

// MemoryMessageStore 内存版消息历史存储
// 基于 sync.RWMutex + map，进程内存储，重启丢失
type MemoryMessageStore struct {
	mu       sync.RWMutex
	messages map[string][]*schema.Message // threadID → 消息列表
}

// NewMemoryMessageStore 创建内存版消息历史存储
func NewMemoryMessageStore() *MemoryMessageStore {
	return &MemoryMessageStore{
		messages: make(map[string][]*schema.Message),
	}
}

// Get 获取指定线程的消息历史（返回防御性副本，防止调用方修改内部状态）
func (s *MemoryMessageStore) Get(ctx stdctx.Context, threadID string) ([]*schema.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msgs, ok := s.messages[threadID]
	if !ok {
		return nil, nil // 空历史，不是错误
	}
	result := make([]*schema.Message, len(msgs))
	copy(result, msgs)
	return result, nil
}

// Append 追加消息到指定线程（存储完整原始消息）
func (s *MemoryMessageStore) Append(ctx stdctx.Context, threadID string, msg *schema.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages[threadID] = append(s.messages[threadID], msg)
	return nil
}

// Clear 清除指定线程的消息历史
func (s *MemoryMessageStore) Clear(ctx stdctx.Context, threadID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.messages, threadID)
	return nil
}
