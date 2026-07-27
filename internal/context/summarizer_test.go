package context

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestLLMContextSummarizer_NormalSummary(t *testing.T) {
	stub := NewStubChatModel("这是对话的摘要内容，包含关键决策。")
	summarizer := NewLLMContextSummarizer(stub)

	msgs := []*schema.Message{
		userMsg("请计算2+3"),
		assistantMsg("2+3=5"),
		userMsg("请计算10*20"),
		assistantMsg("10*20=200"),
	}

	result, err := summarizer.Summarize(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.called != 1 {
		t.Errorf("expected Generate called 1 time, got %d", stub.called)
	}
	if result.Role != schema.User {
		t.Errorf("expected role=User, got %s", result.Role)
	}
	if !strings.HasPrefix(result.Content, "[对话历史摘要]") {
		t.Errorf("expected content to start with '[对话历史摘要]', got '%s'", result.Content)
	}
	if result.Extra == nil || result.Extra["_context_summary"] != true {
		t.Error("expected Extra['_context_summary']=true")
	}
}

func TestLLMContextSummarizer_SummaryError(t *testing.T) {
	stub := NewStubChatModel("")
	stub.err = fmt.Errorf("LLM unavailable")
	summarizer := NewLLMContextSummarizer(stub)

	msgs := []*schema.Message{userMsg("test")}
	_, err := summarizer.Summarize(context.Background(), msgs)
	if err == nil {
		t.Error("expected error when stub returns error")
	}
}

func TestFormatMessagesForSummary_ContentTruncation(t *testing.T) {
	longContent := strings.Repeat("x", 500) // 500 chars, should be truncated to 200
	msgs := []*schema.Message{
		{Role: schema.User, Content: longContent},
	}
	result := formatMessagesForSummary(msgs)

	// Should contain "[user]:" followed by content truncated to 200 chars + "..."
	if !strings.Contains(result, "...") {
		t.Error("expected truncation marker '...' in formatted output")
	}
	// The formatted line for the user message should not contain the full 500 chars
	// It should be: "[user]: " + 200 chars + "..." = ~210 chars for that line
	lines := strings.Split(result, "\n")
	if len(lines) < 1 {
		t.Fatal("expected at least one line")
	}
	userLine := lines[0]
	// "[user]: " = 8 chars, content truncated to 200 + "..." = 203, total ~211
	if len(userLine) > 220 {
		t.Errorf("content not truncated, line length = %d", len(userLine))
	}
}

func TestFormatMessagesForSummary_ToolCallArgsTruncation(t *testing.T) {
	longArgs := strings.Repeat("a", 300) // 300 chars, should be truncated to 100
	msgs := []*schema.Message{
		{
			Role:    schema.Assistant,
			Content: "",
			ToolCalls: []schema.ToolCall{
				{
					ID:   "c1",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      "calc",
						Arguments: longArgs,
					},
				},
			},
		},
	}
	result := formatMessagesForSummary(msgs)

	// Should contain the tool_call line with truncated args
	if !strings.Contains(result, "tool_call: calc(") {
		t.Error("expected tool_call format in output")
	}
	if !strings.Contains(result, "...") {
		t.Error("expected truncation marker '...' for tool call args")
	}
}
