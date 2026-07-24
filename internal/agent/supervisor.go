package agent

import (
	"context"
	"fmt"
	"io"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
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
			Invokable:    reactAgent.Generate,
			Streamable:   reactAgent.Stream,
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
			SystemPrompt:     "你是一个任务路由助手，根据用户请求选择合适的专家Agent来处理。当专家返回结果后，请将专家的具体结果整合到最终回复中，包含所有具体的数值、数据和结论，不要仅回复\"我来处理\"等泛泛之词。直接回答简单问题，复杂问题交给专家。",
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
