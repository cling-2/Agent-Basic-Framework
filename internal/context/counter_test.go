package context

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestDefaultTokenCounter_PlainText(t *testing.T) {
	c := &DefaultTokenCounter{}
	// "Hello World" = 11 chars, 11/4 = 2.75, ceil = 3
	msg := &schema.Message{Role: schema.User, Content: "Hello World"}
	count, err := c.CountMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 tokens for 11 chars, got %d", count)
	}
}

func TestDefaultTokenCounter_EmptyContent(t *testing.T) {
	c := &DefaultTokenCounter{}
	msg := &schema.Message{Role: schema.User, Content: ""}
	count, err := c.CountMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 tokens for empty content, got %d", count)
	}
}

func TestDefaultTokenCounter_LongText(t *testing.T) {
	c := &DefaultTokenCounter{}
	content := strings.Repeat("a", 1000) // 1000 chars / 4 = 250
	msg := &schema.Message{Role: schema.User, Content: content}
	count, err := c.CountMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 250 {
		t.Errorf("expected 250 tokens, got %d", count)
	}
}

func TestDefaultTokenCounter_ToolCalls(t *testing.T) {
	c := &DefaultTokenCounter{}
	msg := &schema.Message{
		Role:    schema.Assistant,
		Content: "",
		ToolCalls: []schema.ToolCall{
			{ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "calc", Arguments: `{}`}},
			{ID: "c2", Type: "function", Function: schema.FunctionCall{Name: "grep", Arguments: `{}`}},
			{ID: "c3", Type: "function", Function: schema.FunctionCall{Name: "hash", Arguments: `{}`}},
		},
	}
	count, err := c.CountMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3*toolCallEstimate {
		t.Errorf("expected %d tokens (3 tool calls), got %d", 3*toolCallEstimate, count)
	}
}

func TestDefaultTokenCounter_Multimodal(t *testing.T) {
	c := &DefaultTokenCounter{}
	msg := &schema.Message{
		Role:         schema.User,
		Content:      "",
		MultiContent: []schema.ChatMessagePart{{Type: schema.ChatMessagePartTypeImageURL}},
	}
	count, err := c.CountMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != multimodalEstimate {
		t.Errorf("expected %d tokens (1 multimodal), got %d", multimodalEstimate, count)
	}
}

func TestDefaultTokenCounter_Mixed(t *testing.T) {
	c := &DefaultTokenCounter{}
	// 20 chars text = 5 tokens, 2 tool calls = 600, 1 multimodal = 2000, total = 2605
	msg := &schema.Message{
		Role:    schema.Assistant,
		Content: strings.Repeat("x", 20),
		ToolCalls: []schema.ToolCall{
			{ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "a", Arguments: `{}`}},
			{ID: "c2", Type: "function", Function: schema.FunctionCall{Name: "b", Arguments: `{}`}},
		},
		MultiContent: []schema.ChatMessagePart{{Type: schema.ChatMessagePartTypeImageURL}},
	}
	count, err := c.CountMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := 5 + 2*toolCallEstimate + multimodalEstimate
	if count != expected {
		t.Errorf("expected %d tokens, got %d", expected, count)
	}
}

func TestDefaultTokenCounter_NilMessage(t *testing.T) {
	c := &DefaultTokenCounter{}
	count, err := c.CountMessage(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 tokens for nil message, got %d", count)
	}
}

func TestDefaultTokenCounter_CountMessages(t *testing.T) {
	c := &DefaultTokenCounter{}
	msgs := []*schema.Message{
		{Role: schema.User, Content: strings.Repeat("a", 40)},     // 10 tokens
		{Role: schema.Assistant, Content: strings.Repeat("b", 80)}, // 20 tokens
	}
	count, err := c.CountMessages(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 30 {
		t.Errorf("expected 30 tokens total, got %d", count)
	}
}
