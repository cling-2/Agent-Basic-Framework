package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/components/model"
	react "github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

const DefaultMaxIterations = 20

// ReActAgentConfig ReAct Agent 配置
type ReActAgentConfig struct {
	// ChatModel 支持 ToolCalling 的 ChatModel
	ChatModel model.ToolCallingChatModel
	// Tools 该 Agent 可用的工具列表
	Tools []tool.BaseTool
	// ACLMiddleware ACL 权限拦截中间件
	ACLMiddleware compose.ToolMiddleware
	// HITLMiddleware HITL 人工审批中间件
	HITLMiddleware compose.ToolMiddleware
	// MaxStep 最大迭代步数（默认 20）
	MaxStep int
	// SystemPrompt 系统提示词（通过 MessageModifier 注入）
	SystemPrompt string
	// GraphName Graph 名称
	GraphName string
}

// NewReActAgent 创建 ReAct Agent
// 封装 Eino react.Agent，注入 ACL 中间件和系统提示词
func NewReActAgent(ctx context.Context, cfg *ReActAgentConfig) (*react.Agent, error) {
	if cfg.ChatModel == nil {
		return nil, fmt.Errorf("chat model is required")
	}

	maxStep := cfg.MaxStep
	if maxStep <= 0 {
		maxStep = DefaultMaxIterations
	}

	// Eino react.Agent 要求至少一个工具，无工具时注册一个 no_op 工具
	tools := cfg.Tools
	if len(tools) == 0 {
		noOp, err := utils.InferTool[NoOpInput, NoOpOutput](
			"no_op",
			"无需工具操作，直接回答问题",
			func(ctx context.Context, _ NoOpInput) (NoOpOutput, error) {
				return NoOpOutput{Message: "无需工具"}, nil
			},
		)
		if err != nil {
			return nil, fmt.Errorf("create no_op tool: %w", err)
		}
		tools = []tool.BaseTool{noOp}
	}

	// 构建中间件链：ACL → HITL
	middlewares := []compose.ToolMiddleware{}
	if cfg.ACLMiddleware.Invokable != nil {
		middlewares = append(middlewares, cfg.ACLMiddleware)
	}
	if cfg.HITLMiddleware.Invokable != nil {
		middlewares = append(middlewares, cfg.HITLMiddleware)
	}

	// 构建 MessageModifier（注入系统提示词）
	var messageModifier react.MessageModifier
	if cfg.SystemPrompt != "" {
		messageModifier = func(_ context.Context, input []*schema.Message) []*schema.Message {
			result := make([]*schema.Message, 0, len(input)+1)
			result = append(result, schema.SystemMessage(cfg.SystemPrompt))
			result = append(result, input...)
			return result
		}
	}

	agentCfg := &react.AgentConfig{
		ToolCallingModel: cfg.ChatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools:               tools,
			ToolCallMiddlewares: middlewares,
			// 未知工具处理：回灌提示而非报错
			UnknownToolsHandler: func(_ context.Context, name, _ string) (string, error) {
				return fmt.Sprintf("工具 %s 不存在，请使用其他可用工具。", name), nil
			},
		},
		MaxStep:         maxStep,
		MessageModifier: messageModifier,
		GraphName:       cfg.GraphName,
	}

	return react.NewAgent(ctx, agentCfg)
}

// NoOpInput / NoOpOutput 无操作工具的输入输出
type NoOpInput struct {
	Message string `json:"message" jsonschema:"description=用户消息"`
}

type NoOpOutput struct {
	Message string `json:"message"`
}
