package agent

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cloudwego/eino/components/model"
	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
)

// LLMConfig 真实LLM配置
// APIKey、BaseURL、Model 留空则回退到 MockChatModel
type LLMConfig struct {
	APIKey     string // API密钥，由用户补充
	BaseURL    string // OpenAI兼容API地址，由用户补充（如 https://api.example.com/v1）
	Model      string // 模型名称，由用户补充（如 gpt-4o、qwen-plus 等）
	HeaderName  string // 自定义HTTP Header名（如 ksyun-code-type）
	HeaderValue string // 自定义HTTP Header值（业务标识）
}

// headerTransport 自定义HTTP Transport，在每次请求时注入自定义Header
type headerTransport struct {
	Transport http.RoundTripper
	Headers   map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}
	return t.Transport.RoundTrip(req)
}

// NewChatModel 根据配置创建真实LLM或回退到MockChatModel
// 如果 LLMConfig.APIKey 为空，则返回 MockChatModel
func NewChatModel(ctx context.Context, cfg *LLMConfig) (model.ToolCallingChatModel, error) {
	// 未配置APIKey时回退到MockChatModel
	if cfg == nil || cfg.APIKey == "" {
		fmt.Println("[LLM] 未配置APIKey，回退到MockChatModel")
		return NewMockChatModel(), nil
	}

	// 构建自定义HTTP Client（注入自定义Header）
	var httpClient *http.Client
	if cfg.HeaderName != "" && cfg.HeaderValue != "" {
		transport := &headerTransport{
			Transport: http.DefaultTransport,
			Headers:   map[string]string{cfg.HeaderName: cfg.HeaderValue},
		}
		httpClient = &http.Client{Transport: transport}
		fmt.Printf("[LLM] 自定义Header: %s=%s\n", cfg.HeaderName, cfg.HeaderValue)
	}

	// 创建OpenAI兼容ChatModel
	chatModel, err := openaimodel.NewChatModel(ctx, &openaimodel.ChatModelConfig{
		APIKey:     cfg.APIKey,
		BaseURL:    cfg.BaseURL,
		Model:      cfg.Model,
		HTTPClient: httpClient,
	})
	if err != nil {
		return nil, fmt.Errorf("创建LLM ChatModel失败: %w", err)
	}

	fmt.Printf("[LLM] 已接入真实LLM: BaseURL=%s, Model=%s\n", cfg.BaseURL, cfg.Model)
	return chatModel, nil
}
