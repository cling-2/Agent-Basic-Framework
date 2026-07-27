package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/cloudwego/eino/components/model"
	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
)

// LLMConfig 真实LLM配置
// APIKey、BaseURL、Model 留空则回退到 MockChatModel
type LLMConfig struct {
	APIKey      string // API密钥，由用户补充
	BaseURL     string // OpenAI兼容API地址，由用户补充（如 https://api.example.com/v1）
	Model       string // 模型名称，由用户补充（如 gpt-4o、qwen-plus 等）
	HeaderName  string // 自定义HTTP Header名（如 ksyun-code-type）
	HeaderValue string // 自定义HTTP Header值（业务标识）
	MaxRetries  int           // 429最大重试次数（0表示使用默认值3，-1表示不重试）
	InitialBackoff time.Duration // 初始退避时长（默认1s）
	MaxBackoff    time.Duration // 最大退避时长（默认30s）
}

// 429重试默认配置
const (
	DefaultMaxRetries     = 5
	DefaultInitialBackoff = 1 * time.Second
	DefaultMaxBackoff     = 30 * time.Second
)

// headerTransport 自定义HTTP Transport，在每次请求时注入自定义Header
// 支持 429 指数退避重试，尊重 Retry-After 头
type headerTransport struct {
	Transport       http.RoundTripper
	Headers         map[string]string
	MaxRetries      int
	InitialBackoff  time.Duration
	MaxBackoff      time.Duration
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// 注入自定义 Header
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}

	// 无重试配置时直接透传
	maxRetries := t.MaxRetries
	if maxRetries == -1 {
		return t.Transport.RoundTrip(req)
	}
	if maxRetries == 0 {
		maxRetries = DefaultMaxRetries
	}

	initialBackoff := t.InitialBackoff
	if initialBackoff == 0 {
		initialBackoff = DefaultInitialBackoff
	}
	maxBackoff := t.MaxBackoff
	if maxBackoff == 0 {
		maxBackoff = DefaultMaxBackoff
	}

	// 缓存请求 body 用于重试（HTTP body 只能读取一次）
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("读取请求body失败: %w", err)
		}
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		req.ContentLength = int64(len(bodyBytes))
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		// 重试时重置 body
		if attempt > 0 && bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			req.ContentLength = int64(len(bodyBytes))
		}

		resp, err := t.Transport.RoundTrip(req)
		if err != nil {
			lastErr = err
			if attempt < maxRetries {
				backoff := calcBackoff(initialBackoff, maxBackoff, attempt)
				log.Printf("[LLM/Retry] 请求失败(attempt %d/%d): %v, 等待 %v 后重试",
					attempt+1, maxRetries+1, err, backoff)
				time.Sleep(backoff)
				continue
			}
			return nil, lastErr
		}

		// 检测 429 限频
		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			resp.Body.Close()

			if attempt < maxRetries {
				backoff := retryAfter
				if backoff == 0 {
					backoff = calcBackoff(initialBackoff, maxBackoff, attempt)
				}
				// 加 jitter（0~25% 退避时长），防止惊群效应
				jitter := time.Duration(float64(backoff) * 0.25 * randFloat())
				backoff += jitter

				log.Printf("[LLM/Retry] 429 Rate Limit(attempt %d/%d), 等待 %v 后重试",
					attempt+1, maxRetries+1, backoff)
				time.Sleep(backoff)
				continue
			}

			// 重试耗尽，返回错误
			log.Printf("[LLM/Retry] 429 Rate Limit: 重试耗尽(%d次)", maxRetries+1)
			return nil, fmt.Errorf("LLM API rate limited (429): retries exhausted after %d attempts", maxRetries+1)
		}

		// 成功（非429状态码），直接返回
		return resp, nil
	}

	return nil, lastErr
}

// calcBackoff 计算指数退避时长: initial * 2^attempt，上限为 maxBackoff
func calcBackoff(initial, maxBackoff time.Duration, attempt int) time.Duration {
	backoff := initial * time.Duration(1<<uint(attempt))
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	return backoff
}

// parseRetryAfter 解析 Retry-After HTTP 头
// 支持整数秒和 HTTP 日期两种格式
func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	// 尝试解析为整数秒
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}
	// 尝试解析为 HTTP 日期（RFC 850 / RFC 1123）
	if t, err := http.ParseTime(value); err == nil {
		duration := time.Until(t)
		if duration > 0 {
			return duration
		}
	}
	return 0
}

// randFloat 返回 0~1 之间的随机浮点数
func randFloat() float64 {
	return rand.Float64()
}

// NewChatModel 根据配置创建真实LLM或回退到MockChatModel
// 如果 LLMConfig.APIKey 为空，则返回 MockChatModel
func NewChatModel(ctx context.Context, cfg *LLMConfig) (model.ToolCallingChatModel, error) {
	// 未配置APIKey时回退到MockChatModel
	if cfg == nil || cfg.APIKey == "" {
		fmt.Println("[LLM] 未配置APIKey，回退到MockChatModel")
		return NewMockChatModel(), nil
	}

	// 构建自定义HTTP Client（注入自定义Header + 429重试逻辑）
	var httpClient *http.Client
	needsCustomTransport := (cfg.HeaderName != "" && cfg.HeaderValue != "") || cfg.MaxRetries != -1

	if needsCustomTransport {
		headers := map[string]string{}
		if cfg.HeaderName != "" && cfg.HeaderValue != "" {
			headers[cfg.HeaderName] = cfg.HeaderValue
			fmt.Printf("[LLM] 自定义Header: %s=%s\n", cfg.HeaderName, cfg.HeaderValue)
		}
		transport := &headerTransport{
			Transport:       http.DefaultTransport,
			Headers:         headers,
			MaxRetries:      cfg.MaxRetries,
			InitialBackoff:  cfg.InitialBackoff,
			MaxBackoff:      cfg.MaxBackoff,
		}
		httpClient = &http.Client{Transport: transport}
		if cfg.MaxRetries != -1 {
			retries := cfg.MaxRetries
			if retries == 0 {
				retries = DefaultMaxRetries
			}
			fmt.Printf("[LLM] 429重试配置: MaxRetries=%d, InitialBackoff=%v, MaxBackoff=%v\n",
				retries, cfg.InitialBackoff, cfg.MaxBackoff)
		}
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
