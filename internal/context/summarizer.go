package context

import (
	stdctx "context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ContextSummarizer 上下文摘要压缩器接口
type ContextSummarizer interface {
	// Summarize 将旧消息压缩为一段摘要
	// messages: 待摘要的旧消息列表（可操作区间内的旧消息，不含 system 和保留区间）
	// 返回: 摘要消息（user 角色，Extra 中标记为摘要类型）
	Summarize(ctx stdctx.Context, messages []*schema.Message) (*schema.Message, error)
}

// LLMContextSummarizer 基于 LLM 的上下文摘要压缩器
// 调用 ChatModel.Generate 生成摘要文本
type LLMContextSummarizer struct {
	chatModel model.BaseChatModel
}

// NewLLMContextSummarizer 创建基于 LLM 的摘要压缩器
func NewLLMContextSummarizer(chatModel model.BaseChatModel) *LLMContextSummarizer {
	return &LLMContextSummarizer{chatModel: chatModel}
}

const summarySystemPrompt = `你是一个对话摘要助手。请将以下对话历史压缩为一段简洁的摘要，要求：
1. 保留关键信息、决策和结论
2. 保留涉及的工具调用及其结果摘要
3. 丢弃冗余细节和重复内容
4. 摘要长度不超过 500 字
5. 使用清晰的中文表述`

const summaryUserPromptTemplate = `请对以下对话历史进行摘要压缩：

%s`

// Summarize 将旧消息压缩为一段摘要
func (s *LLMContextSummarizer) Summarize(ctx stdctx.Context, messages []*schema.Message) (*schema.Message, error) {
	// 构建对话文本
	conversationText := formatMessagesForSummary(messages)

	prompt := fmt.Sprintf(summaryUserPromptTemplate, conversationText)
	summaryMessages := []*schema.Message{
		schema.SystemMessage(summarySystemPrompt),
		schema.UserMessage(prompt),
	}

	result, err := s.chatModel.Generate(ctx, summaryMessages)
	if err != nil {
		return nil, fmt.Errorf("summarize generation failed: %w", err)
	}

	// 返回摘要消息（user 角色，Extra 标记为摘要类型）
	return &schema.Message{
		Role:    schema.User,
		Content: fmt.Sprintf("[对话历史摘要]\n%s", result.Content),
		Extra:   map[string]any{"_context_summary": true},
	}, nil
}

// formatMessagesForSummary 将消息列表格式化为摘要输入文本
// 每条消息内容截断至 200 字符，ToolCall 信息保留函数名和参数
func formatMessagesForSummary(messages []*schema.Message) string {
	var builder strings.Builder
	for _, msg := range messages {
		roleLabel := string(msg.Role)
		content := msg.Content
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		builder.WriteString(fmt.Sprintf("[%s]: %s\n", roleLabel, content))

		// ToolCalls 信息
		for _, tc := range msg.ToolCalls {
			args := tc.Function.Arguments
			if len(args) > 100 {
				args = args[:100] + "..."
			}
			builder.WriteString(fmt.Sprintf("  [tool_call: %s(%s)]\n", tc.Function.Name, args))
		}
	}
	return builder.String()
}
