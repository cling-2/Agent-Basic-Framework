package hitl

import (
	"context"
	"sync"
	"time"
)

// ApprovalStore 待审批状态存储
// 独立于请求 context 生命周期，数据操作不受 context 取消影响
type ApprovalStore struct {
	mu       sync.RWMutex
	cards    map[string]*InterruptCard // key: threadID
	stopCh   chan struct{}
}

// NewApprovalStore 创建审批存储，启动后台过期清理
func NewApprovalStore() *ApprovalStore {
	s := &ApprovalStore{
		cards:  make(map[string]*InterruptCard),
		stopCh: make(chan struct{}),
	}
	go s.cleanupExpired()
	return s
}

// AddApproval 添加待审批卡片（使用 context.Background()，不依赖请求 context）
func (s *ApprovalStore) AddApproval(_ context.Context, threadID string, card *InterruptCard) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cards[threadID] = card
}

// GetApproval 获取待审批卡片，过期则自动删除并返回 nil
func (s *ApprovalStore) GetApproval(_ context.Context, threadID string) (*InterruptCard, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	card, ok := s.cards[threadID]
	if !ok {
		return nil, false
	}
	if card.IsExpired() {
		delete(s.cards, threadID)
		return nil, false
	}
	return card, true
}

// RemoveApproval 移除审批卡片
func (s *ApprovalStore) RemoveApproval(_ context.Context, threadID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cards, threadID)
}

// ListApprovals 列出所有未过期的待审批卡片
func (s *ApprovalStore) ListApprovals(_ context.Context) []*InterruptCard {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	var result []*InterruptCard
	for _, card := range s.cards {
		if now.Before(card.ExpiresAt) {
			result = append(result, card)
		}
	}
	return result
}

// Close 停止后台清理 goroutine
func (s *ApprovalStore) Close() {
	close(s.stopCh)
}

// cleanupExpired 定期清理过期的审批卡片
// 使用 time.Ticker 驱动，独立于任何请求 context
func (s *ApprovalStore) cleanupExpired() {
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

// ---------- MemoryCheckpointStore ----------
// 实现 Eino compose.CheckPointStore 接口，供未来扩展使用

// MemoryCheckpointStore 内存版检查点存储
type MemoryCheckpointStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemoryCheckpointStore 创建内存检查点存储
func NewMemoryCheckpointStore() *MemoryCheckpointStore {
	return &MemoryCheckpointStore{
		data: make(map[string][]byte),
	}
}

// Get 实现 compose.CheckPointStore 接口
func (s *MemoryCheckpointStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.data[id]
	return data, ok, nil
}

// Set 实现 compose.CheckPointStore 接口
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
