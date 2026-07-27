package agent

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/schema"
)

// messageCollectorKey context key for MessageCollector
type messageCollectorKey struct{}

// MessageCollector 收集 Agent 执行过程中的中间消息（ToolCall + ToolResult）
// 用于在执行完毕后统一存储到 MessageStore，确保多轮对话中 LLM 能看到工具交互历史
// 线程安全，支持多个 Specialist 并发写入
type MessageCollector struct {
	mu       sync.Mutex
	messages []*schema.Message
	wg       sync.WaitGroup // 等待 streaming goroutine 完成
}

// NewMessageCollector 创建 MessageCollector
func NewMessageCollector() *MessageCollector {
	return &MessageCollector{
		messages: make([]*schema.Message, 0),
	}
}

// Add 追加消息到收集器（线程安全）
func (c *MessageCollector) Add(msgs ...*schema.Message) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, msgs...)
}

// Messages 返回所有已收集消息的防御性副本
func (c *MessageCollector) Messages() []*schema.Message {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]*schema.Message, len(c.messages))
	copy(result, c.messages)
	return result
}

// Wait 等待所有通过 wg.Add 注册的 goroutine 完成
func (c *MessageCollector) Wait() {
	if c == nil {
		return
	}
	c.wg.Wait()
}

// AddDelta 注册一个异步消息收集任务（用于 streaming 模式）
// 返回的 done 函数必须在 goroutine 完成时调用
func (c *MessageCollector) AddDelta() func() {
	if c == nil {
		return func() {}
	}
	c.wg.Add(1)
	return c.wg.Done
}

// WithMessageCollector 将 MessageCollector 注入 context
func WithMessageCollector(ctx context.Context, collector *MessageCollector) context.Context {
	return context.WithValue(ctx, messageCollectorKey{}, collector)
}

// GetMessageCollector 从 context 获取 MessageCollector，不存在时返回 nil
func GetMessageCollector(ctx context.Context) *MessageCollector {
	v, _ := ctx.Value(messageCollectorKey{}).(*MessageCollector)
	return v
}
