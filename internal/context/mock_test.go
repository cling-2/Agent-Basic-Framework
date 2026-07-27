package context

import (
	stdctx "context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ========== Mock TokenCounter ==========

// MockTokenCounter 可控的 Token 计数器 Mock
// counts: 消息 Content → 固定 token 数；未命中时使用 defaultCount
type MockTokenCounter struct {
	counts       map[string]int
	defaultCount int
}

func NewMockTokenCounter(defaultCount int) *MockTokenCounter {
	return &MockTokenCounter{
		counts:       make(map[string]int),
		defaultCount: defaultCount,
	}
}

func (m *MockTokenCounter) Set(content string, tokens int) {
	m.counts[content] = tokens
}

func (m *MockTokenCounter) CountMessage(_ stdctx.Context, msg *schema.Message) (int, error) {
	if msg == nil {
		return 0, nil
	}
	if count, ok := m.counts[msg.Content]; ok {
		return count, nil
	}
	return m.defaultCount, nil
}

func (m *MockTokenCounter) CountMessages(ctx stdctx.Context, messages []*schema.Message) (int, error) {
	total := 0
	for _, msg := range messages {
		count, err := m.CountMessage(ctx, msg)
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

// ========== Stub ChatModel ==========

// StubChatModel 固定响应的 ChatModel 桩实现
type StubChatModel struct {
	response  string
	called    int
	lastInput []*schema.Message
	err       error // 可选：让 Generate 返回 error
}

func NewStubChatModel(response string) *StubChatModel {
	return &StubChatModel{response: response}
}

func (s *StubChatModel) Generate(_ stdctx.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	s.called++
	s.lastInput = input
	if s.err != nil {
		return nil, s.err
	}
	return schema.AssistantMessage(s.response, nil), nil
}

func (s *StubChatModel) Stream(ctx stdctx.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := s.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

// ========== Message Constructors ==========

func sysMsg(content string) *schema.Message {
	return schema.SystemMessage(content)
}

func userMsg(content string) *schema.Message {
	return schema.UserMessage(content)
}

func assistantMsg(content string) *schema.Message {
	return schema.AssistantMessage(content, nil)
}

func toolCallMsg(name, args, callID string) *schema.Message {
	return &schema.Message{
		Role:    schema.Assistant,
		Content: "",
		ToolCalls: []schema.ToolCall{
			{
				ID:   callID,
				Type: "function",
				Function: schema.FunctionCall{
					Name:      name,
					Arguments: args,
				},
			},
		},
	}
}

func toolResultMsg(content, callID string) *schema.Message {
	return &schema.Message{
		Role:       schema.Tool,
		Content:    content,
		ToolCallID: callID,
	}
}

func makePair(name, args, callID, result string) []*schema.Message {
	return []*schema.Message{
		toolCallMsg(name, args, callID),
		toolResultMsg(result, callID),
	}
}

// makeConversation 构造 N 轮 User+Assistant 对话
// contentPrefix 用于区分每条消息，同时作为 MockTokenCounter 的查找键
func makeConversation(n int, contentPrefix string) []*schema.Message {
	var msgs []*schema.Message
	for i := 0; i < n; i++ {
		msgs = append(msgs, userMsg(fmt.Sprintf("%s-user-%d", contentPrefix, i)))
		msgs = append(msgs, assistantMsg(fmt.Sprintf("%s-assistant-%d", contentPrefix, i)))
	}
	return msgs
}
