package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// MockChatModel 确定性的模拟 ChatModel
// 实现模型。ToolCallingChatModel 接口，用于无需真实 LLM API 的演示场景
//
// 路由逻辑（基于消息内容的简单规则）：
//   - Supervisor 模式：根据用户消息选择 Specialist
//   - ReAct 模式：根据消息内容选择工具调用
//   - 通用问答：直接生成自然回复
//   - 工具结果：组装最终文本回复
type MockChatModel struct {
	toolInfos []*schema.ToolInfo
}

// NewMockChatModel 创建模拟 ChatModel
func NewMockChatModel() *MockChatModel {
	return &MockChatModel{}
}

// Generate 同步生成回复
func (m *MockChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return m.generate(ctx, input), nil
}

// Stream 流式生成回复
func (m *MockChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg := m.generate(ctx, input)
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

// WithTools 绑定工具信息（返回新实例，不修改原实例）
func (m *MockChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return &MockChatModel{
		toolInfos: tools,
	}, nil
}

// generate 核心路由逻辑
func (m *MockChatModel) generate(_ context.Context, input []*schema.Message) *schema.Message {
	// 查找最后一个用户消息
	var lastUserMsg string
	var hasToolResult bool

	for i := len(input) - 1; i >= 0; i-- {
		msg := input[i]
		if msg.Role == schema.User && msg.Content != "" {
			lastUserMsg = msg.Content
			break
		}
		if msg.Role == schema.Tool {
			hasToolResult = true
		}
	}

	// 场景1：已有工具结果，生成最终文本回复
	if hasToolResult {
		return m.generateFinalReply(input)
	}

	// 场景2：Supervisor 模式 —— 有 Specialist 类工具
	if m.isSupervisorMode() {
		return m.routeToSpecialist(lastUserMsg)
	}

	// 场景3：ReAct 模式 —— 根据消息内容选择工具
	if matched := m.selectToolCall(lastUserMsg); matched != nil {
		return matched
	}

	// 场景4：无匹配工具，通用问答（直接自然回复）
	return m.generateGeneralReply(lastUserMsg)
}

// isSupervisorMode 判断是否处于 Supervisor 模式（工具名 = Agent 名称）
func (m *MockChatModel) isSupervisorMode() bool {
	for _, info := range m.toolInfos {
		if strings.HasSuffix(info.Name, "Agent") {
			return true
		}
	}
	return false
}

// ---------- Supervisor 模式路由 ----------

func (m *MockChatModel) routeToSpecialist(msg string) *schema.Message {
	msgLower := strings.ToLower(msg)

	// 管理员工具 → AdminAgent
	if containsAny(msgLower, "哈希", "hash", "md5", "sha256", "sha", "加密", "摘要",
		"邮件", "email", "发送邮件", "发邮件", "send") {
		return m.makeToolCall("AdminAgent", fmt.Sprintf(`{"message":"%s"}`, msg))
	}

	// 数学相关 → MathAgent
	if containsAny(msgLower, "计算", "算", "加", "减", "乘", "除", "+", "-", "*", "/", "数学", "求和", "等于") {
		return m.makeToolCall("MathAgent", fmt.Sprintf(`{"message":"%s"}`, msg))
	}

	// 搜索/查找 → SearchAgent
	if containsAny(msgLower, "搜索", "查找", "文件", "grep", "search", "find", "文件内容") {
		return m.makeToolCall("SearchAgent", fmt.Sprintf(`{"message":"%s"}`, msg))
	}

	// 默认 → Host 直接回答（不做 Specialist 路由）
	return m.generateGeneralReply(msg)
}

// ---------- ReAct 模式工具选择 ----------

// selectToolCall 根据消息内容选择工具调用
// 返回 nil 表示无匹配工具，应由 generateGeneralReply 处理
func (m *MockChatModel) selectToolCall(msg string) *schema.Message {
	msgLower := strings.ToLower(msg)

	for _, info := range m.toolInfos {
		name := strings.ToLower(info.Name)

		// 计算器
		if strings.Contains(name, "calculator") && containsAny(msgLower, "计算", "算", "加", "减", "乘", "除", "+", "-", "*", "/", "数学", "等于") {
			expr := extractMathExpr(msg)
			return m.makeToolCall(info.Name, fmt.Sprintf(`{"expression":"%s"}`, expr))
		}

		// Grep 文件搜索
		if strings.Contains(name, "grep") && containsAny(msgLower, "grep", "文件内容", "搜索文件", "搜索", "查找") {
			return m.makeToolCall(info.Name, fmt.Sprintf(`{"pattern":"%s"}`, msg))
		}

		// 哈希计算
		if strings.Contains(name, "hash_compute") && containsAny(msgLower, "哈希", "hash", "md5", "sha256", "sha", "加密", "摘要") {
			algo := "sha256"
			if containsAny(msgLower, "md5") {
				algo = "md5"
			}
			return m.makeToolCall(info.Name, fmt.Sprintf(`{"text":"%s","algorithm":"%s"}`, msg, algo))
		}
				// 发送邮件
				if strings.Contains(name, "send_email") && containsAny(msgLower, "邮件", "email", "发送", "send", "发邮件") {
					return m.makeToolCall(info.Name, `{"to":"user@example.com","subject":"通知","body":"这是一封测试邮件"}`)
				}
	}

	// 无匹配工具，返回 nil 让 generateGeneralReply 处理
	return nil
}

// ---------- 通用问答 ----------

// generateGeneralReply 对日常对话、知识问答等通用请求生成自然回复
// 区别于工具调用，这是纯文本回复，不需要调用任何工具
func (m *MockChatModel) generateGeneralReply(msg string) *schema.Message {
	msgLower := strings.ToLower(msg)

	// 根据消息内容生成合理的自然回复
	switch {
	case containsAny(msgLower, "你好", "hello", "hi", "嗨", "您好"):
		return schema.AssistantMessage("你好！我是 Kingsoft Agent 助手，可以帮你进行数学计算、文件搜索、哈希计算、邮件发送等任务。请问有什么可以帮你的？", nil)

	case containsAny(msgLower, "谢谢", "感谢", "thanks", "thank"):
		return schema.AssistantMessage("不客气！如果还有其他问题，随时可以问我。", nil)

	case containsAny(msgLower, "你是谁", "你是什么", "who are you", "介绍一下"):
		return schema.AssistantMessage("我是 Kingsoft Agent 框架的演示助手。我基于 Eino 框架构建，支持多 Agent 协作编排和工具调用。当前可用的专家包括：MathAgent（数学计算）、SearchAgent（文件搜索）、AdminAgent（哈希计算与邮件发送）等。", nil)

	case containsAny(msgLower, "能做什么", "功能", "帮助", "help", "可以做什么"):
		return schema.AssistantMessage("我可以帮你完成以下任务：\n• 🔢 数学计算（如「计算2+3*4」）\n• 📁 文件搜索（如「grep TODO」）\n• 🔐 哈希计算（如「计算hello的SHA256」）\n• 💬 日常对话和知识问答\n\n请直接告诉我你想做什么！", nil)

	default:
		// 通用回复：自然地回应用户的问题
		return schema.AssistantMessage(fmt.Sprintf("这是一个好问题！关于「%s」，我目前没有专门的工具来处理，但我可以作为通用助手与你交流。你可以试试问我数学计算、文件搜索或哈希计算等我能使用工具完成的任务。", msg), nil)
	}
}

// ---------- 工具结果处理 ----------

func (m *MockChatModel) generateFinalReply(input []*schema.Message) *schema.Message {
	var toolResults []string
	for _, msg := range input {
		if msg.Role == schema.Tool && msg.Content != "" {
			toolResults = append(toolResults, msg.Content)
		}
	}

	// 检查是否有权限拒绝
	for _, result := range toolResults {
		if strings.Contains(result, "权限不足") {
			return schema.AssistantMessage(result, nil)
		}
	}

	// 尝试从工具输出中提取 summary 字段，生成人类可读回复
	var summaries []string
	for _, result := range toolResults {
		summary := extractSummary(result)
		if summary != "" {
			summaries = append(summaries, summary)
		}
	}

	if len(summaries) > 0 {
		reply := strings.Join(summaries, "\n\n")
		return schema.AssistantMessage(reply, nil)
	}

	// 回退：使用原始结果
	if len(toolResults) > 0 {
		reply := strings.Join(toolResults, "\n")
		return schema.AssistantMessage(reply, nil)
	}

	return schema.AssistantMessage("我已处理您的请求。", nil)
}

// extractSummary 从工具输出的JSON中提取 summary 字段
func extractSummary(toolOutput string) string {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(toolOutput), &obj); err != nil {
		return toolOutput // 非JSON，直接返回原文
	}
	if summary, ok := obj["summary"].(string); ok && summary != "" {
		return summary
	}
	return toolOutput // 没有 summary 字段，返回原文
}

// ---------- 工具调用构造 ----------

func (m *MockChatModel) makeToolCall(toolName, arguments string) *schema.Message {
	return &schema.Message{
		Role:    schema.Assistant,
		Content: "",
		ToolCalls: []schema.ToolCall{
			{
				ID:   fmt.Sprintf("call_%s_001", toolName),
				Type: "function",
				Function: schema.FunctionCall{
					Name:      toolName,
					Arguments: arguments,
				},
			},
		},
	}
}

// ---------- 辅助函数 ----------

func containsAny(s string, keywords ...string) bool {
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

// extractMathExpr 从消息中提取数学表达式
func extractMathExpr(msg string) string {
	var buf strings.Builder
	hasDigit := false
	for _, ch := range msg {
		if ch >= '0' && ch <= '9' || ch == '.' {
			buf.WriteRune(ch)
			hasDigit = true
		} else if ch == '+' || ch == '-' || ch == '*' || ch == '/' || ch == '(' || ch == ')' {
			buf.WriteRune(ch)
		} else if hasDigit && buf.Len() > 0 {
			break
		}
	}
	expr := strings.TrimSpace(buf.String())
	if expr == "" {
		return "1+1"
	}
	return expr
}
