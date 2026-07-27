package context

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// helper: create a defaultContextManager with MockTokenCounter injected
func newTestManager(config ContextManagerConfig, counter TokenCounter) *defaultContextManager {
	var summarizer ContextSummarizer
	if config.ChatModel != nil && config.SummaryThreshold > 0 {
		summarizer = NewLLMContextSummarizer(config.ChatModel)
	}
	return &defaultContextManager{
		config:     config,
		counter:    counter,
		summarizer: summarizer,
	}
}

func TestProcess_FastShortCircuit(t *testing.T) {
	counter := NewMockTokenCounter(50) // 50 tokens per msg
	mgr := newTestManager(ContextManagerConfig{
		MaxMessages: 20,
		MaxTokens:   8000,
	}, counter)

	msgs := makeConversation(3, "msg") // 6 messages, 300 tokens
	result, err := mgr.Process(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != len(msgs) {
		t.Errorf("fast short-circuit: expected %d messages, got %d", len(msgs), len(result))
	}
}

func TestProcess_TriggerSummary(t *testing.T) {
	stub := NewStubChatModel("摘要：用户进行了数学计算。")
	counter := NewMockTokenCounter(100)
	mgr := newTestManager(ContextManagerConfig{
		MaxMessages:      20,
		MaxTokens:        800,
		SummaryThreshold: 0.8, // threshold = 640 tokens
		ChatModel:        stub,
	}, counter)

	// 10 rounds = 20 messages = 2000 tokens > 640 threshold
	msgs := makeConversation(10, "msg")
	result, err := mgr.Process(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.called == 0 {
		t.Error("expected summarizer to be called")
	}
	// Result should contain a summary message
	hasSummary := false
	for _, m := range result {
		if strings.HasPrefix(m.Content, "[对话历史摘要]") {
			hasSummary = true
		}
	}
	if !hasSummary {
		t.Error("expected summary message in result")
	}
}

func TestProcess_SummaryDegradation(t *testing.T) {
	stub := NewStubChatModel("")
	stub.err = fmt.Errorf("LLM unavailable")
	counter := NewMockTokenCounter(100)
	mgr := newTestManager(ContextManagerConfig{
		MaxMessages:      20,
		MaxTokens:        800,
		SummaryThreshold: 0.8,
		ChatModel:        stub,
	}, counter)

	msgs := makeConversation(10, "msg")
	result, err := mgr.Process(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should degrade to trim-only, not lose all messages
	if len(result) == 0 {
		t.Error("degradation should not lose all messages")
	}
}

func TestProcess_TrimOnlyNoSummary(t *testing.T) {
	counter := NewMockTokenCounter(100)
	mgr := newTestManager(ContextManagerConfig{
		MaxMessages: 20,
		MaxTokens:   500,
		// ChatModel = nil → no summarizer
	}, counter)

	msgs := makeConversation(10, "msg") // 20 msgs, 2000 tokens
	result, err := mgr.Process(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	totalTokens, _ := counter.CountMessages(context.Background(), result)
	if totalTokens > 500 {
		t.Errorf("expected tokens <= 500 after trim, got %d", totalTokens)
	}
}

func TestProcess_FinalGuard(t *testing.T) {
	counter := NewMockTokenCounter(0)
	// Build messages where each has a different token count
	msgs := []*schema.Message{
		userMsg("a"), assistantMsg("b"),
		userMsg("c"), assistantMsg("d"),
		userMsg("e"), assistantMsg("f"),
	}
	counter.Set("a", 300)
	counter.Set("b", 300)
	counter.Set("c", 300)
	counter.Set("d", 300)
	counter.Set("e", 300)
	counter.Set("f", 300)

	mgr := newTestManager(ContextManagerConfig{
		MaxMessages: 20,
		MaxTokens:   500,
	}, counter)

	result, err := mgr.Process(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	totalTokens, _ := counter.CountMessages(context.Background(), result)
	if totalTokens > 500 {
		t.Errorf("FINAL_GUARD should enforce token limit, got %d tokens", totalTokens)
	}
}

func TestProcess_SystemMessagesPersist(t *testing.T) {
	stub := NewStubChatModel("摘要内容")
	counter := NewMockTokenCounter(100)
	mgr := newTestManager(ContextManagerConfig{
		MaxMessages:      20,
		MaxTokens:        800,
		SummaryThreshold: 0.8,
		ChatModel:        stub,
	}, counter)

	msgs := []*schema.Message{
		sysMsg("你是一个助手"),
	}
	msgs = append(msgs, makeConversation(10, "msg")...)

	result, err := mgr.Process(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("expected non-empty result")
	}
	if result[0].Role != schema.System {
		t.Errorf("system message should be at position 0, got role=%s", result[0].Role)
	}
}

func TestProcess_PairIntegrity(t *testing.T) {
	counter := NewMockTokenCounter(0)
	pair := makePair("calc", `{"expr":"1+1"}`, "call_1", "2")
	counter.Set(pair[0].Content, 200) // toolCallMsg
	counter.Set(pair[1].Content, 100) // toolResultMsg
	counter.Set("q1", 300)
	counter.Set("a1", 300)

	msgs := []*schema.Message{
		userMsg("q1"),
		assistantMsg("a1"),
		pair[0],
		pair[1],
	}

	mgr := newTestManager(ContextManagerConfig{
		MaxMessages: 20,
		MaxTokens:   350,
	}, counter)

	result, err := mgr.Process(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// If either toolCall or toolResult is present, both must be
	hasTC := false
	hasTR := false
	for _, m := range result {
		if m.Role == schema.Assistant && len(m.ToolCalls) > 0 {
			hasTC = true
		}
		if m.Role == schema.Tool {
			hasTR = true
		}
	}
	if hasTC != hasTR {
		t.Errorf("pair integrity violated: hasToolCall=%v hasToolResult=%v", hasTC, hasTR)
	}
}

func TestProcess_UnlimitedConfig(t *testing.T) {
	counter := NewMockTokenCounter(100)
	mgr := newTestManager(ContextManagerConfig{
		MaxMessages: 0, // unlimited
		MaxTokens:   0, // unlimited
	}, counter)

	msgs := makeConversation(50, "msg") // 100 messages, 10000 tokens
	result, err := mgr.Process(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != len(msgs) {
		t.Errorf("unlimited config should not trim, got %d/%d messages", len(result), len(msgs))
	}
}

func TestProcess_ThresholdBoundary(t *testing.T) {
	counter := NewMockTokenCounter(0)
	// MaxTokens=1000, SummaryThreshold=0.8, threshold = 800
	// Exactly 800 tokens → should NOT trigger summary (needs strict >)
	counter.Set("msg1", 400)
	counter.Set("msg2", 400)

	stub := NewStubChatModel("摘要")
	mgr := newTestManager(ContextManagerConfig{
		MaxMessages:      20,
		MaxTokens:        1000,
		SummaryThreshold: 0.8,
		ChatModel:        stub,
	}, counter)

	msgs := []*schema.Message{userMsg("msg1"), assistantMsg("msg2")}
	result, err := mgr.Process(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.called > 0 {
		t.Error("should NOT trigger summary when tokens exactly equal threshold (needs strict >)")
	}
	if len(result) != len(msgs) {
		t.Errorf("at threshold boundary, messages should pass through, got %d/%d", len(result), len(msgs))
	}
}

func TestProcess_EmptyInput(t *testing.T) {
	counter := NewMockTokenCounter(100)
	mgr := newTestManager(ContextManagerConfig{
		MaxMessages: 20,
		MaxTokens:   8000,
	}, counter)

	result, err := mgr.Process(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("nil input should return nil/empty, got %d messages", len(result))
	}
}
