package context

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestTrimByToken_NoTrimNeeded(t *testing.T) {
	counter := NewMockTokenCounter(100) // each message = 100 tokens
	msgs := makeConversation(3, "msg")  // 6 messages, 600 tokens total
	result := TrimByToken(msgs, 800, counter)
	if len(result) != len(msgs) {
		t.Errorf("expected %d messages (no trim), got %d", len(msgs), len(result))
	}
}

func TestTrimByToken_TrimToLimit(t *testing.T) {
	counter := NewMockTokenCounter(100) // each message = 100 tokens
	msgs := makeConversation(10, "msg") // 20 messages, 2000 tokens total
	result := TrimByToken(msgs, 600, counter) // should keep ~6 messages from tail

	totalTokens, _ := counter.CountMessages(nil, result)
	if totalTokens > 600 {
		t.Errorf("expected tokens <= 600, got %d", totalTokens)
	}
	// Should retain the most recent messages
	lastMsg := result[len(result)-1]
	if lastMsg.Content != "msg-assistant-9" {
		t.Errorf("expected last message 'msg-assistant-9', got '%s'", lastMsg.Content)
	}
}

func TestTrimByToken_SystemAlwaysRetained(t *testing.T) {
	counter := NewMockTokenCounter(100)
	msgs := []*schema.Message{
		sysMsg("system-prompt"),
		userMsg("q1"),
		assistantMsg("a1"),
		userMsg("q2"),
		assistantMsg("a2"),
	}
	result := TrimByToken(msgs, 100, counter) // 1 message budget
	if len(result) == 0 {
		t.Fatal("expected non-empty result")
	}
	if string(result[0].Role) != "system" {
		t.Errorf("system message not retained at position 0, got role=%s", result[0].Role)
	}
}

func TestTrimByToken_PairNotSplit(t *testing.T) {
	counter := NewMockTokenCounter(100)
	pair := makePair("calc", `{"expr":"1+1"}`, "call_1", "2") // 2 msgs, 200 tokens
	msgs := []*schema.Message{
		userMsg("q1"),
		assistantMsg("a1"),
		pair[0], // toolCallMsg - 200 tokens
		pair[1], // toolResultMsg - 100 tokens (pair total = 300)
	}
	// Set pair messages to have higher token counts to test non-split
	counter.Set(pair[0].Content, 200)
	counter.Set(pair[1].Content, 100)

	result := TrimByToken(msgs, 300, counter)
	// If pair is retained, both toolCall and toolResult must be present
	hasTC := false
	hasTR := false
	for _, m := range result {
		if string(m.Role) == "assistant" && len(m.ToolCalls) > 0 {
			hasTC = true
		}
		if string(m.Role) == "tool" {
			hasTR = true
		}
	}
	if hasTC && !hasTR {
		t.Error("ToolCall retained without corresponding ToolResult — pair was split")
	}
}

func TestTrimByToken_FirstGroupKeptEvenIfOverBudget(t *testing.T) {
	counter := NewMockTokenCounter(0)
	// Single massive message that exceeds maxTokens
	bigMsg := userMsg("big-message")
	counter.Set("big-message", 5000)

	msgs := []*schema.Message{bigMsg}
	result := TrimByToken(msgs, 1000, counter)
	if len(result) == 0 {
		t.Error("expected at least 1 message retained even if over budget")
	}
}

func TestTrimByToken_ZeroMaxTokens(t *testing.T) {
	counter := NewMockTokenCounter(100)
	msgs := makeConversation(5, "msg")
	result := TrimByToken(msgs, 0, counter)
	if len(result) != len(msgs) {
		t.Errorf("maxTokens=0 should return all messages, got %d/%d", len(result), len(msgs))
	}
}

func TestAggressiveDropOldest_DropsFirstNonSystem(t *testing.T) {
	msgs := []*schema.Message{
		sysMsg("sys"),
		userMsg("old-q"),
		assistantMsg("old-a"),
		userMsg("new-q"),
		assistantMsg("new-a"),
	}
	result := aggressiveDropOldest(msgs)

	// groupMessages treats each non-ToolCall message as an independent group,
	// so "old-q" (user) is the first group and gets dropped alone.
	// Result: sys + old-a + new-q + new-a = 4 messages
	if len(result) != 4 {
		t.Fatalf("expected 4 messages after drop, got %d", len(result))
	}
	if string(result[0].Role) != "system" {
		t.Error("system message should be retained")
	}
	if result[1].Content != "old-a" {
		t.Errorf("expected 'old-a' at position 1, got '%s'", result[1].Content)
	}
}

func TestAggressiveDropOldest_OnlyOneGroup(t *testing.T) {
	msgs := []*schema.Message{
		sysMsg("sys"),
		userMsg("only-q"),
	}
	result := aggressiveDropOldest(msgs)
	// Can't drop the only non-system group
	if len(result) != len(msgs) {
		t.Errorf("should retain at least 1 non-system group, got %d msgs", len(result))
	}
}

func TestAggressiveDropOldest_NoNonSystemMessages(t *testing.T) {
	msgs := []*schema.Message{sysMsg("sys")}
	result := aggressiveDropOldest(msgs)
	if len(result) != 1 {
		t.Errorf("should retain system message, got %d msgs", len(result))
	}
}
