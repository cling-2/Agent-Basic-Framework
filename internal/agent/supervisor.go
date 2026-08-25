package agent

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	react "github.com/cloudwego/eino/flow/agent/react"
	host "github.com/cloudwego/eino/flow/agent/multiagent/host"
	"github.com/cloudwego/eino/schema"
)

// SpecialistDef 专家 Agent 定义
type SpecialistDef struct {
	// Name Agent 名称（同时作为 Host 侧的工具名）
	Name string
	// IntendedUse 用途描述（作为 Host 侧的工具描述）
	IntendedUse string
	// SystemPrompt 系统提示词
	SystemPrompt string
	// ToolNames 该 Agent 可用的工具名称列表
	ToolNames []string
}

// SupervisorConfig Supervisor Agent 配置
type SupervisorConfig struct {
	// HostModel Host 编排者的 ChatModel
	HostModel model.ToolCallingChatModel
	// Specialists 专家 Agent 定义列表
	Specialists []*SpecialistDef
	// ACLMiddleware ACL 权限拦截中间件
	ACLMiddleware compose.ToolMiddleware
	// CreateReActAgent 创建 ReAct Agent 的工厂函数（可注入用于测试）
	CreateReActAgent func(ctx context.Context, cfg *ReActAgentConfig) (*react.Agent, error)
}

// AgentInfo Agent 信息（用于 API 返回）
type AgentInfo struct {
	Name        string `json:"name"`
	IntendedUse string `json:"intended_use"`
}

// BuildSpecialists 构建专家 Agent 列表
// toolLookup: 根据工具名称列表查找已注册的工具
func BuildSpecialists(
	ctx context.Context,
	chatModel model.ToolCallingChatModel,
	defs []*SpecialistDef,
	toolLookup func(names []string) []tool.BaseTool,
	aclMiddleware compose.ToolMiddleware,
	hitlMiddleware compose.ToolMiddleware,
) ([]*react.Agent, []*host.Specialist, error) {

	var agents []*react.Agent
	var specialists []*host.Specialist

	for _, def := range defs {
		baseTools := toolLookup(def.ToolNames)

		reactAgent, err := NewReActAgent(ctx, &ReActAgentConfig{
			ChatModel:     chatModel,
			Tools:         baseTools,
			ACLMiddleware: aclMiddleware,
			HITLMiddleware: hitlMiddleware,
			MaxStep:       DefaultMaxIterations,
			SystemPrompt:  def.SystemPrompt,
			GraphName:     def.Name,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("create specialist %s failed: %w", def.Name, err)
		}

		agents = append(agents, reactAgent)
		specialists = append(specialists, &host.Specialist{
			AgentMeta: host.AgentMeta{
				Name:        def.Name,
				IntendedUse: def.IntendedUse,
			},
			SystemPrompt: def.SystemPrompt,
			Invokable:    wrapSpecialistGenerate(reactAgent),
			Streamable:   wrapSpecialistStream(reactAgent),
		})
	}

	return agents, specialists, nil
}

// CreateSupervisor 从已构建的 Specialists 创建 Supervisor
func CreateSupervisor(
	ctx context.Context,
	hostModel model.ToolCallingChatModel,
	specialists []*host.Specialist,
) (*host.MultiAgent, error) {
	return host.NewMultiAgent(ctx, &host.MultiAgentConfig{
		Host: host.Host{
			ToolCallingModel: hostModel,
			SystemPrompt: `你是一个任务路由 Supervisor。你的唯一职责是判断用户请求属于哪个专家 Agent 的能力范围，然后【必须调用对应专家 Agent 工具】来处理，绝不可自己代答需要使用工具的任务。

可用专家 Agent（作为工具调用）：
- MathAgent：数学计算、算术运算（如「计算」「等于」「加减乘除」）
- SearchAgent：文件内容搜索、模式匹配（如「搜索」「grep」「查找文件」）
- AdminAgent：管理员操作，包括哈希计算（「哈希」「SHA256」「MD5」）和邮件发送（「发邮件」「email」「邮件」）

规则：
1. 凡是涉及计算、搜索、哈希、邮件的任务，【必须调用对应专家 Agent 工具】，禁止仅用文字回复「我来处理/我来发送/我来计算」等。不调用专家工具而直接回答，视为严重错误。
2. 只有纯闲聊、问候、与工具无关的通用知识问题时，才可直接文字回答。
3. 专家返回结果后，将具体结果（数值、哈希值、发送状态等）完整整合到最终回复中，不可省略具体数据。

示例：
用户：发送一封邮件给 alice@example.com，主题：项目进展
正确：调用 AdminAgent 工具（系统自动处理审批）
错误：回复「好的，我来为您发送邮件」（❌未调用工具）

用户：计算 2+3*4
正确：调用 MathAgent 工具
错误：回复「我来帮你计算」（❌未调用工具）`,
		},
		Specialists: specialists,
		Name:        "SupervisorAgent",
		// 自定义 StreamToolCallChecker：遍历完整流后再判断是否存在 ToolCall
		// 默认的 firstChunkStreamToolCallChecker 只看第一个非空 chunk，
		// 当模型先输出文本再输出 ToolCalls 时（如 Claude、国产模型）会误判为"直接回答"，
		// 导致专家 Agent 从未被调用，最终答案丢失。
		StreamToolCallChecker: func(_ context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error) {
			defer sr.Close()

			hasToolCall := false
			for {
				msg, err := sr.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return false, err
				}
				if len(msg.ToolCalls) > 0 {
					hasToolCall = true
				}
			}
			return hasToolCall, nil
		},
		Summarizer: &host.Summarizer{
			ChatModel:    hostModel,
			SystemPrompt: "将多个专家的回复合并为一段连贯的最终回复。必须保留每个专家回复中的具体数值、结果和关键结论，不要省略具体数据。",
		},
	})
}


// ---------- Specialist æ¶æ¯æè·åè£å¨ ----------

// wrapSpecialistGenerate åè£ Specialist ç Generate æ¹æ³
// ä½¿ç¨ WithMessageFuture æè·ææä¸­é´æ¶æ¯ï¼ToolCall + ToolResult + æç»åå¤ï¼
// éè¿ context ä¸­ç MessageCollector æ¶éï¼ä¾ handler å¨æ§è¡å®æ¯åå­å¨
func wrapSpecialistGenerate(ra *react.Agent) func(ctx context.Context, input []*schema.Message, opts ...agent.AgentOption) (*schema.Message, error) {
	return func(ctx context.Context, input []*schema.Message, opts ...agent.AgentOption) (*schema.Message, error) {
		collector := GetMessageCollector(ctx)
		if collector != nil {
			opt, future := react.WithMessageFuture()
			allOpts := append(opts, opt)
			result, err := ra.Generate(ctx, input, allOpts...)
			if err != nil {
				return nil, err
			}

			// ä» MessageFuture æ¶éææä¸­é´æ¶æ¯
			iter := future.GetMessages()
			for {
				msg, ok, iterErr := iter.Next()
				if !ok || iterErr != nil {
					break
				}
				if msg != nil {
					collector.Add(msg)
				}
			}

			return result, nil
		}

		// æ  collector æ¶éçº§ä¸ºç´æ¥è°ç¨ï¼ä¿æç°æè¡ä¸ºï¼
		return ra.Generate(ctx, input, opts...)
	}
}

// wrapSpecialistStream åè£ Specialist ç Stream æ¹æ³
// ä½¿ç¨ WithMessageFuture æè·ææä¸­é´æ¶æ¯
// streaming æ¨¡å¼ä¸æ¶æ¯éè¿å¼æ­¥ goroutine æ¶éï¼future.GetMessageStreamsï¼
func wrapSpecialistStream(ra *react.Agent) func(ctx context.Context, input []*schema.Message, opts ...agent.AgentOption) (*schema.StreamReader[*schema.Message], error) {
	return func(ctx context.Context, input []*schema.Message, opts ...agent.AgentOption) (*schema.StreamReader[*schema.Message], error) {
		collector := GetMessageCollector(ctx)
		if collector != nil {
			opt, future := react.WithMessageFuture()
			allOpts := append(opts, opt)
			stream, err := ra.Stream(ctx, input, allOpts...)
			if err != nil {
				return nil, err
			}

			// å¼æ­¥æ¶éä¸­é´æ¶æ¯ï¼streaming æ¨¡å¼ä¸ MessageFuture éè¿ iterator åå streamï¼
			done := collector.AddDelta()
			go func() {
				defer done()
				sIter := future.GetMessageStreams()
				for {
					sr, ok, iterErr := sIter.Next()
					if !ok || iterErr != nil {
						break
					}
					if sr != nil {
						msg, concatErr := schema.ConcatMessageStream(sr)
						if concatErr == nil && msg != nil {
							collector.Add(msg)
						}
						if concatErr != nil {
							log.Printf("[Specialist/Stream] æ¶æ¯æµåå¹¶å¤±è´¥: %v", concatErr)
						}
					}
				}
			}()

			return stream, nil
		}

		// æ  collector æ¶éçº§ä¸ºç´æ¥è°ç¨
		return ra.Stream(ctx, input, opts...)
	}
}
