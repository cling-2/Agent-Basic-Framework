package context

import (
	stdctx "context"
	"fmt"

	"github.com/cloudwego/eino/schema"
)

// TokenCounter Token 计数器接口
// 仅用于裁剪判断和快速短路，不用于 API 计费，精确度要求不高
type TokenCounter interface {
	// CountMessages 计算多条消息的总 Token 数
	CountMessages(ctx stdctx.Context, messages []*schema.Message) (int, error)

	// CountMessage 计算单条消息的 Token 数
	CountMessage(ctx stdctx.Context, msg *schema.Message) (int, error)
}

// 估算常量
const (
	charsPerToken     = 4   // 约 4 字符 ≈ 1 token
	multimodalEstimate = 2000 // 每个多模态项固定估算 token 数
	toolCallEstimate   = 300  // 每个 ToolCall 固定估算 token 数（含函数名 + 参数）
)

// DefaultTokenCounter 默认 Token 计数器（粗粒度估算）
// 策略：将消息可见文本总长度 ÷ 4，多模态和 ToolCall 额外加固定估值
// 不逐字段细分（Content+ReasoningContent 合计，ToolCalls 每项固定 300）
type DefaultTokenCounter struct{}

// CountMessage 计算单条消息的 Token 数（粗粒度估算）
func (c *DefaultTokenCounter) CountMessage(ctx stdctx.Context, msg *schema.Message) (int, error) {
	if msg == nil {
		return 0, nil
	}

	// 文本总量：Content + ReasoningContent 合计
	textLen := len(msg.Content) + len(msg.ReasoningContent)
	tokens := textLen / charsPerToken
	if textLen % charsPerToken > 0 {
		tokens++
	}

	// 多模态：每项固定估值
	tokens += len(msg.MultiContent) * multimodalEstimate
	tokens += len(msg.UserInputMultiContent) * multimodalEstimate

	// ToolCalls：每项固定估值
	tokens += len(msg.ToolCalls) * toolCallEstimate

	return tokens, nil
}

// CountMessages 计算多条消息的总 Token 数
func (c *DefaultTokenCounter) CountMessages(ctx stdctx.Context, messages []*schema.Message) (int, error) {
	total := 0
	for _, msg := range messages {
		count, err := c.CountMessage(ctx, msg)
		if err != nil {
			return 0, fmt.Errorf("count message failed: %w", err)
		}
		total += count
	}
	return total, nil
}
