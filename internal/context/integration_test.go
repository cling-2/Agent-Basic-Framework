//go:build integration

package context

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/cloudwego/eino/components/model"
	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
)

func newRealChatModel(t *testing.T) model.BaseChatModel {
	t.Helper()
	apiKey := os.Getenv("LLM_API_KEY")
	baseURL := os.Getenv("LLM_BASE_URL")
	modelName := os.Getenv("LLM_MODEL")
	if apiKey == "" {
		t.Skip("LLM_API_KEY not set, skipping integration test")
	}

	chatModel, err := openaimodel.NewChatModel(context.Background(), &openaimodel.ChatModelConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   modelName,
	})
	if err != nil {
		t.Fatalf("failed to create chat model: %v", err)
	}
	return chatModel
}

func TestIntegration_RealSummary(t *testing.T) {
	chatModel := newRealChatModel(t)

	// 构造 15 轮对话
	var msgs []*schema.Message
	for i := 0; i < 15; i++ {
		msgs = append(msgs, userMsg(fmt.Sprintf("请计算 %d + %d 的结果", i*10, i*10+1)))
		msgs = append(msgs, assistantMsg(fmt.Sprintf("%d + %d = %d", i*10, i*10+1, i*20+1)))
	}

	summarizer := NewLLMContextSummarizer(chatModel)
	result, err := summarizer.Summarize(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	if result.Content == "" {
		t.Error("expected non-empty summary")
	}
	if !startsWith(result.Content, "[对话历史摘要]") {
		t.Errorf("summary should start with [对话历史摘要], got: %s", result.Content[:min(50, len(result.Content))])
	}
	t.Logf("Summary: %s", result.Content)
}

func TestIntegration_LongConversationEndToEnd(t *testing.T) {
	chatModel := newRealChatModel(t)

	// 构造 30+ 轮对话，超出 8000 token
	var msgs []*schema.Message
	msgs = append(msgs, sysMsg("你是一个数学计算助手"))
	for i := 0; i < 35; i++ {
		msgs = append(msgs, userMsg(fmt.Sprintf("第%d轮：请帮我计算 %d * %d", i+1, i+2, i+3)))
		msgs = append(msgs, assistantMsg(fmt.Sprintf("第%d轮：%d * %d = %d", i+1, i+2, i+3, (i+2)*(i+3))))
	}

	mgr := NewContextManager(ContextManagerConfig{
		MaxMessages:      20,
		MaxTokens:        8000,
		SummaryThreshold: 0.8,
		ChatModel:        chatModel,
	})

	result, err := mgr.Process(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	// Result should be significantly shorter than input
	if len(result) >= len(msgs) {
		t.Errorf("expected trimmed result (%d msgs < %d input)", len(result), len(msgs))
	}

	// System message must be retained
	if len(result) > 0 && result[0].Role != schema.System {
		t.Error("system message should be at position 0")
	}

	t.Logf("Input: %d msgs → Output: %d msgs", len(msgs), len(result))
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
