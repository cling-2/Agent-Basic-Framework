package agent

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"kingsoft-agent/internal/auth"
	ctxmgr "kingsoft-agent/internal/context"
	"kingsoft-agent/internal/hitl"
	"kingsoft-agent/internal/memory"
	"kingsoft-agent/internal/toolreg"
	pkgmodel "kingsoft-agent/pkg/model"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	flowagent "github.com/cloudwego/eino/flow/agent"
	host "github.com/cloudwego/eino/flow/agent/multiagent/host"
	"github.com/cloudwego/eino/schema"

	"github.com/gin-gonic/gin"
)

// AgentTimeout 单次 Agent 执行超时
const AgentTimeout = 180 * time.Second

// ---------- 请求/响应结构体 ----------

// ChatRequest Agent 对话请求
type ChatRequest struct {
	ThreadID string `json:"thread_id" binding:"required"`
	Message  string `json:"message" binding:"required"`
}

// ChatResponse Agent 对话响应
type ChatResponse struct {
	Reply     string              `json:"reply"`
	ThreadID  string              `json:"thread_id"`
	Interrupt *hitl.InterruptInfo `json:"interrupt,omitempty"` // 非 nil 表示执行被中断
}

// ResumeRequest 审批决策请求
type ResumeRequest struct {
	Decision string `json:"decision" binding:"required"` // "approve" 或 "reject"
	Comment  string `json:"comment"`                     // 拒绝原因（可选）
}

// CheckpointsResponse 待审批检查点列表响应
type CheckpointsResponse struct {
	Checkpoints []*hitl.InterruptCard `json:"checkpoints"`
}

// ToolInfoResponse 工具信息响应
type ToolInfoResponse struct {
	Tools []ToolItem `json:"tools"`
}

// ToolItem 工具项
type ToolItem struct {
	Name        string `json:"name"`
	Description string `json:"desc"`
}

// AgentsResponse Agent 列表响应
type AgentsResponse struct {
	Agents []AgentInfo `json:"agents"`
}

// ErrorDetail 错误详情
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ErrorResponse 错误响应
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// 常用错误码
var (
	errAgentTimeout = ErrorDetail{Code: "AGENT_TIMEOUT", Message: "agent execution timed out"}
	errAgentError   = ErrorDetail{Code: "AGENT_ERROR", Message: "agent execution failed"}
	errBadRequest   = ErrorDetail{Code: "BAD_REQUEST", Message: "invalid request parameters"}
	errForbidden    = ErrorDetail{Code: "FORBIDDEN", Message: "insufficient permissions"}
)

// ---------- AgentHandler ----------

// AgentHandler Agent 相关 HTTP 接口处理器
type AgentHandler struct {
	mu                sync.RWMutex
	supervisor        *host.MultiAgent
	toolRegistry      *toolreg.ToolRegistry
	aclChecker        auth.ACLChecker
	agentDefs         []*SpecialistDef
	approvalStore     *hitl.ApprovalStore
	intentRiskChecker IntentRiskChecker // 意图风险兜底检查器
	messageStore      ctxmgr.MessageStore
	contextManager    ctxmgr.ContextManager
	memoryStore       memory.MemoryStore     // 长期记忆存储
	memoryExtractor   memory.MemoryExtractor // LLM 记忆提取器
}

// NewAgentHandler 创建 Agent 处理器
func NewAgentHandler(
	supervisor *host.MultiAgent,
	toolRegistry *toolreg.ToolRegistry,
	aclChecker auth.ACLChecker,
	agentDefs []*SpecialistDef,
	approvalStore *hitl.ApprovalStore,
	intentRiskChecker IntentRiskChecker,
	messageStore ctxmgr.MessageStore,
	contextManager ctxmgr.ContextManager,
	memoryStore memory.MemoryStore,
	memoryExtractor memory.MemoryExtractor,
) *AgentHandler {
	return &AgentHandler{
		supervisor:        supervisor,
		toolRegistry:      toolRegistry,
		aclChecker:        aclChecker,
		agentDefs:         agentDefs,
		approvalStore:     approvalStore,
		intentRiskChecker: intentRiskChecker,
		messageStore:      messageStore,
		contextManager:    contextManager,
		memoryStore:       memoryStore,
		memoryExtractor:   memoryExtractor,
	}
}

// Chat 与 Agent 对话
// POST /api/agent/chat
// 流程：加载完整原始历史 → 追加用户消息到 Store → Process(裁剪) → Generate(裁剪版) → 追加回复到 Store
func (h *AgentHandler) Chat(c *gin.Context) {
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: errBadRequest})
		return
	}

	// 验证 UserContext（已由 AuthMiddleware 注入）
	uc, ok := pkgmodel.UserContextFromCtx(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: ErrorDetail{
			Code: "UNAUTHORIZED", Message: "session invalid or expired",
		}})
		return
	}

	// 创建超时 context
	ctx, cancel := context.WithTimeout(c.Request.Context(), AgentTimeout)
	defer cancel()

	// 注入 threadID 和原始消息到 context（供 HITL 中间件使用）
	ctx = hitl.WithThreadID(ctx, req.ThreadID)
	ctx = hitl.WithOriginalMessage(ctx, req.Message)

	// 注入 MessageCollector（供 Specialist 包装器捕获 ToolCall/ToolResult 中间消息）
	collector := NewMessageCollector()
	ctx = WithMessageCollector(ctx, collector)

	// 加载完整原始历史（MessageStore 存全量，不裁剪）
	history, err := h.messageStore.Get(ctx, req.ThreadID)
	if err != nil {
		log.Printf("[Context] 加载历史失败: %v", err)
		history = nil // 降级：使用空历史
	}

	// 追加当前用户消息到 Store（完整存储，不裁剪）
	userMsg := schema.UserMessage(req.Message)
	_ = h.messageStore.Append(ctx, req.ThreadID, userMsg) // 写入失败不影响当前

	// 构造完整消息列表（用于 Process 输入）
	var fullMessages []*schema.Message
	if history != nil {
		fullMessages = append(fullMessages, history...)
	}
	fullMessages = append(fullMessages, userMsg)

	// 加载长期记忆并注入到消息列表头部
	if h.memoryStore != nil {
		memoryEntries := memory.BuildMemoryInjectionForUser(h.memoryStore, uc.UserID)
		memoryMsg := memory.BuildMemoryInjection(memoryEntries)
		if memoryMsg != nil {
			fullMessages = append([]*schema.Message{memoryMsg}, fullMessages...)
		}
	}

	// 上下文管理：Process 输出仅用于本次 LLM 调用，不回写 Store
	trimmedPrompt, processErr := h.contextManager.Process(ctx, fullMessages)
	if processErr != nil {
		log.Printf("[Context] 上下文处理失败: %v，降级使用原始消息", processErr)
		trimmedPrompt = fullMessages // 降级：使用原始消息
	}

	// 读取 supervisor（读锁保护）
	h.mu.RLock()
	supervisor := h.supervisor
	h.mu.RUnlock()

	// 调用 Supervisor（仅传裁剪版 Prompt）
	result, err := supervisor.Generate(ctx, trimmedPrompt)
	if err != nil {
		// 检测是否为 HITL 中断错误
		if info, existed := compose.ExtractInterruptInfo(err); existed {
			// 存储中断前已收集的消息（ToolCall 记录有价值）
			allMessages := collector.Messages()
			for _, msg := range allMessages {
				_ = h.messageStore.Append(ctx, req.ThreadID, msg)
			}
			// 存储合成中断消息，用于历史恢复时还原中断状态
			_ = h.messageStore.Append(ctx, req.ThreadID, schema.AssistantMessage("⏸️ 操作需要人工审批，请在下方审批面板中确认。", nil))
			h.handleInterrupt(c, req, info)
			return
		}

		if ctx.Err() == context.DeadlineExceeded {
			c.JSON(http.StatusGatewayTimeout, ErrorResponse{Error: errAgentTimeout})
			return
		}

		// 记录详细错误日志
		errMsg := err.Error()
		log.Printf("[Agent] 执行失败: %v", err)

		// 提取对用户友好的错误信息
		userMsg := formatAgentError(errMsg)

		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: ErrorDetail{
			Code:    "AGENT_ERROR",
			Message: userMsg,
		}})
		return
	}

	// 存储 Agent 执行过程中的所有中间消息（ToolCall + ToolResult）
	allMessages := collector.Messages()
	for _, msg := range allMessages {
		_ = h.messageStore.Append(ctx, req.ThreadID, msg)
	}
	// 存储 Agent 最终回复
	if result != nil {
		_ = h.messageStore.Append(ctx, req.ThreadID, result)
	}

	// 提取回复内容
	reply := ""
	if result != nil {
		reply = result.Content
	}

	c.JSON(http.StatusOK, ChatResponse{
		Reply:    reply,
		ThreadID: req.ThreadID,
	})

	// 后置：检测是否需要写入长期记忆（降级容错，不影响对话）
	if h.memoryStore != nil && reply != "" {
		memory.SaveMemoryFromConversation(h.memoryStore, uc.UserID, req.Message, h.memoryExtractor)
	}
}

// handleInterrupt 处理 HITL 中断响应
func (h *AgentHandler) handleInterrupt(c *gin.Context, req ChatRequest, info *compose.InterruptInfo) {
	// 从 InterruptContexts 中提取审批信息
	for _, ic := range info.InterruptContexts {
		approvalInfo, ok := ic.Info.(hitl.ApprovalInfo)
		if !ok {
			continue
		}

		// 审批卡片已在中间件中保存到 ApprovalStore
		// 此处构造前端中断响应
		c.JSON(http.StatusOK, ChatResponse{
			Reply:    "⏸️ 操作需要人工审批，请在下方审批面板中确认。",
			ThreadID: req.ThreadID,
			Interrupt: &hitl.InterruptInfo{
				InterruptID: ic.ID,
				ToolName:    approvalInfo.ToolName,
				ToolInput:   approvalInfo.ToolInput,
				RiskReason:  approvalInfo.RiskReason,
			},
		})
		return
	}

	// 未识别的中断类型，当作普通错误处理
	c.JSON(http.StatusInternalServerError, ErrorResponse{Error: ErrorDetail{
		Code:    "AGENT_ERROR",
		Message: "agent execution interrupted with unknown reason",
	}})
}

// Resume 处理审批决策，恢复 Agent 执行
// POST /api/agent/checkpoint/:thread_id/decide
func (h *AgentHandler) Resume(c *gin.Context) {
	threadID := c.Param("thread_id")

	var req ResumeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: errBadRequest})
		return
	}

	// 验证 UserContext
	uc, ok := pkgmodel.UserContextFromCtx(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: ErrorDetail{
			Code: "UNAUTHORIZED", Message: "session invalid or expired",
		}})
		return
	}

	// 获取待审批卡片（使用 context.Background()，不依赖请求 context）
	bgCtx := context.Background()
	card, found := h.approvalStore.GetApproval(bgCtx, threadID)
	if !found {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: ErrorDetail{
			Code: "NOT_FOUND", Message: "no pending approval found for this thread",
		}})
		return
	}

	// 安全校验：审批人与中断发起人一致
	if uc.UserID != card.ApprovalInfo.UserID {
		c.JSON(http.StatusForbidden, ErrorResponse{Error: ErrorDetail{
			Code: "FORBIDDEN", Message: "approval belongs to another user",
		}})
		return
	}

	// 检查过期
	if card.IsExpired() {
		h.approvalStore.RemoveApproval(bgCtx, threadID)
		c.JSON(http.StatusGone, ErrorResponse{Error: ErrorDetail{
			Code: "EXPIRED", Message: "approval has expired",
		}})
		return
	}

	// 移除审批状态（先移除，避免重入）
	h.approvalStore.RemoveApproval(bgCtx, threadID)

	// 注入审批决策到 context（使用 context.Background() + WithTimeout，不依赖请求 context）
	resumeCtx, cancel := context.WithTimeout(context.Background(), AgentTimeout)
	defer cancel()

	resumeCtx = hitl.WithApprovalDecision(resumeCtx, &hitl.ApprovalDecisionCtx{
		ThreadID: threadID,
		ToolName: card.ApprovalInfo.ToolName,
		Decision: req.Decision,
		Comment:  req.Comment,
	})
	resumeCtx = hitl.WithThreadID(resumeCtx, threadID)
	resumeCtx = hitl.WithOriginalMessage(resumeCtx, card.OriginalMessage)

	collector := NewMessageCollector()
	resumeCtx = WithMessageCollector(resumeCtx, collector)

	// 注入 UserContext 到 resume context（中间件需要）
	resumeCtx = pkgmodel.WithUserContext(resumeCtx, uc)

	// 构造引导消息：原始消息 + 审批决策提示，引导 LLM 保持一致
	var guidance string
	if req.Decision == hitl.DecisionApprove {
		guidance = fmt.Sprintf("[系统提示] 用户已批准对工具 %s 的调用（参数：%s）。请继续执行该操作。",
			card.ApprovalInfo.ToolName, card.ApprovalInfo.ToolInput)
	} else {
		reason := req.Comment
		if reason == "" {
			reason = "用户拒绝了此操作"
		}
		guidance = fmt.Sprintf("[系统提示] 用户已拒绝工具 %s 的调用。原因：%s。请告知用户操作被拒绝。",
			card.ApprovalInfo.ToolName, reason)
	}

	messages := []*schema.Message{
		schema.UserMessage(card.OriginalMessage + "\n\n" + guidance),
	}

	// 存储审批决策引导消息，确保历史中 assistant/user 交替出现
	guidanceMsg := schema.UserMessage(guidance)
	_ = h.messageStore.Append(resumeCtx, threadID, guidanceMsg)

	// 上下文管理（恢复时也需裁剪，虽然通常只有 1 条消息会快速短路）
	trimmed, processErr := h.contextManager.Process(resumeCtx, messages)
	if processErr != nil {
		log.Printf("[Context] 恢复时上下文处理失败: %v", processErr)
		trimmed = messages
	}

	// 读取 supervisor
	h.mu.RLock()
	supervisor := h.supervisor
	h.mu.RUnlock()

	// 重新调用 Supervisor（使用裁剪版 Prompt）
	result, err := supervisor.Generate(resumeCtx, trimmed)
	if err != nil {
		// 恢复时也可能产生新的中断
		if info, existed := compose.ExtractInterruptInfo(err); existed {
			h.handleInterrupt(c, ChatRequest{ThreadID: threadID, Message: card.OriginalMessage}, info)
			return
		}

		log.Printf("[Agent] 恢复执行失败: %v", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: ErrorDetail{
			Code:    "AGENT_ERROR",
			Message: "resume execution failed",
		}})
		return
	}

	// 存储 Agent 执行过程中的所有中间消息（ToolCall + ToolResult）
	allMessages := collector.Messages()
	for _, msg := range allMessages {
		_ = h.messageStore.Append(resumeCtx, threadID, msg)
	}
	// 存储 Agent 最终回复
	if result != nil {
		_ = h.messageStore.Append(resumeCtx, threadID, result)
	}

	reply := ""
	if result != nil {
		reply = result.Content
	}

	c.JSON(http.StatusOK, ChatResponse{
		Reply:    reply,
		ThreadID: threadID,
	})
}

// ListCheckpoints 列出待审批检查点
// GET /api/agent/checkpoints
func (h *AgentHandler) ListCheckpoints(c *gin.Context) {
	approvals := h.approvalStore.ListApprovals(context.Background())
	c.JSON(http.StatusOK, CheckpointsResponse{Checkpoints: approvals})
}

// formatAgentError 将内部错误转为用户友好的提示
func formatAgentError(errMsg string) string {
	lower := strings.ToLower(errMsg)

	// LLM API 连接/认证错误
	if strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "no such host") ||
		strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "i/o timeout") {
		return "LLM服务连接失败，请检查 Base URL 是否正确且服务可达。可在「⚙️ 设置」中修改配置。"
	}

	if strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "401") ||
		strings.Contains(lower, "invalid api key") ||
		strings.Contains(lower, "incorrect api key") {
		return "LLM API Key 认证失败，请检查 API Key 是否正确。可在「⚙️ 设置」中修改配置。"
	}

	if strings.Contains(lower, "403") || strings.Contains(lower, "forbidden") {
		return "LLM API 访问被拒绝（403），请检查账号权限。"
	}

	if strings.Contains(lower, "404") || strings.Contains(lower, "model_not_found") ||
		strings.Contains(lower, "model not found") {
		return "LLM 模型不存在，请检查模型名称是否正确。可在「⚙️ 设置」中修改配置。"
	}

	if strings.Contains(lower, "429") || strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "quota") {
		return "LLM API 调用频率超限，请稍后重试。"
	}

	if strings.Contains(lower, "500") || strings.Contains(lower, "internal server error") {
		return "LLM 服务内部错误，请稍后重试。"
	}

	// 通用 NodeRunError
	if strings.Contains(errMsg, "NodeRunError") {
		return "Agent 执行出错，可能是 LLM 服务不可用或配置有误。请在「⚙️ 设置」中检查 LLM 配置，或清空 API Key 使用 Mock 模式。"
	}

	return "Agent 执行失败: " + errMsg
}

// ListTools 列出所有已注册工具（管理端，需 admin 角色）
// GET /api/tools
func (h *AgentHandler) ListTools(c *gin.Context) {
	uc, ok := pkgmodel.UserContextFromCtx(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: ErrorDetail{
			Code: "UNAUTHORIZED", Message: "session invalid or expired",
		}})
		return
	}

	if uc.Role != pkgmodel.RoleAdmin {
		c.JSON(http.StatusForbidden, ErrorResponse{Error: errForbidden})
		return
	}

	infos, err := h.toolRegistry.ToolInfos(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: ErrorDetail{
			Code: "INTERNAL_ERROR", Message: "failed to list tools",
		}})
		return
	}

	tools := make([]ToolItem, 0, len(infos))
	for _, info := range infos {
		tools = append(tools, ToolItem{
			Name:        info.Name,
			Description: info.Desc,
		})
	}

	c.JSON(http.StatusOK, ToolInfoResponse{Tools: tools})
}

// ListAgents 列出所有已注册 Agent（管理端，需 admin 角色）
// GET /api/agents
func (h *AgentHandler) ListAgents(c *gin.Context) {
	uc, ok := pkgmodel.UserContextFromCtx(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: ErrorDetail{
			Code: "UNAUTHORIZED", Message: "session invalid or expired",
		}})
		return
	}

	if uc.Role != pkgmodel.RoleAdmin {
		c.JSON(http.StatusForbidden, ErrorResponse{Error: errForbidden})
		return
	}

	agents := make([]AgentInfo, 0, len(h.agentDefs))
	for _, def := range h.agentDefs {
		agents = append(agents, AgentInfo{
			Name:        def.Name,
			IntendedUse: def.IntendedUse,
		})
	}

	c.JSON(http.StatusOK, AgentsResponse{Agents: agents})
}

// specialistNameSet 返回所有 Specialist 名称集合（用于 SSE 路由检测）
func (h *AgentHandler) specialistNameSet() map[string]bool {
	names := make(map[string]bool, len(h.agentDefs))
	for _, def := range h.agentDefs {
		names[def.Name] = true
	}
	return names
}

// ---------- 流式审批恢复 ----------

// ResumeStream 流式审批恢复处理
// GET /api/agent/checkpoint/:thread_id/decide/stream?decision=approve/reject&comment=xxx
// 使用 Stream() + SSE Callback 实现流式恢复，让前端能看到恢复后的工具执行步骤和结果
func (h *AgentHandler) ResumeStream(c *gin.Context) {
	threadID := c.Param("thread_id")
	decision := c.Query("decision")
	comment := c.Query("comment")

	if decision != hitl.DecisionApprove && decision != hitl.DecisionReject {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: errBadRequest})
		return
	}

	// 验证 UserContext
	uc, ok := pkgmodel.UserContextFromCtx(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: ErrorDetail{
			Code: "UNAUTHORIZED", Message: "session invalid or expired",
		}})
		return
	}

	// 获取待审批卡片
	bgCtx := context.Background()
	card, found := h.approvalStore.GetApproval(bgCtx, threadID)
	if !found {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: ErrorDetail{
			Code: "NOT_FOUND", Message: "no pending approval found for this thread",
		}})
		return
	}

	// 安全校验：审批人与中断发起人一致
	if uc.UserID != card.ApprovalInfo.UserID {
		c.JSON(http.StatusForbidden, ErrorResponse{Error: ErrorDetail{
			Code: "FORBIDDEN", Message: "approval belongs to another user",
		}})
		return
	}

	// 检查过期
	if card.IsExpired() {
		h.approvalStore.RemoveApproval(bgCtx, threadID)
		c.JSON(http.StatusGone, ErrorResponse{Error: ErrorDetail{
			Code: "EXPIRED", Message: "approval has expired",
		}})
		return
	}

	// 移除审批状态（先移除，避免重入）
	h.approvalStore.RemoveApproval(bgCtx, threadID)

	// 设置 SSE 响应头
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.(http.Flusher).Flush()

	// 创建 SSE 发射器和 Callback
	emitter := newSSEEmitter(c, threadID, h.specialistNameSet())
	cb := emitter.BuildCallback()

	// 注入审批决策到 context
	resumeCtx, cancel := context.WithTimeout(context.Background(), AgentTimeout)
	defer cancel()

	resumeCtx = hitl.WithApprovalDecision(resumeCtx, &hitl.ApprovalDecisionCtx{
		ThreadID: threadID,
		ToolName: card.ApprovalInfo.ToolName,
		Decision: decision,
		Comment:  comment,
	})
	resumeCtx = hitl.WithThreadID(resumeCtx, threadID)
	resumeCtx = hitl.WithOriginalMessage(resumeCtx, card.OriginalMessage)
	resumeCtx = pkgmodel.WithUserContext(resumeCtx, uc)

	collector := NewMessageCollector()
	resumeCtx = WithMessageCollector(resumeCtx, collector)

	// 构造引导消息
	var guidance string
	if decision == hitl.DecisionApprove {
		guidance = fmt.Sprintf("[系统提示] 用户已批准对工具 %s 的调用（参数：%s）。请继续执行该操作。",
			card.ApprovalInfo.ToolName, card.ApprovalInfo.ToolInput)
	} else {
		reason := comment
		if reason == "" {
			reason = "用户拒绝了此操作"
		}
		guidance = fmt.Sprintf("[系统提示] 用户已拒绝工具 %s 的调用。原因：%s。请告知用户操作被拒绝。",
			card.ApprovalInfo.ToolName, reason)
	}

	messages := []*schema.Message{
		schema.UserMessage(card.OriginalMessage + "\n\n" + guidance),
	}
	// 存储审批决策引导消息，确保历史中 assistant/user 交替出现
	guidanceMsg := schema.UserMessage(guidance)
	_ = h.messageStore.Append(resumeCtx, threadID, guidanceMsg)

	// 上下文管理
	trimmed, processErr := h.contextManager.Process(resumeCtx, messages)
	if processErr != nil {
		log.Printf("[Context] 恢复流式上下文处理失败: %v", processErr)
		trimmed = messages
	}

	// 注入 Callback
	opts := []flowagent.AgentOption{
		flowagent.WithComposeOptions(compose.WithCallbacks(cb)),
	}

	// 读取 supervisor
	h.mu.RLock()
	supervisor := h.supervisor
	h.mu.RUnlock()

	// 使用 Stream() 进行流式恢复
	stream, streamErr := supervisor.Stream(resumeCtx, trimmed, opts...)
	if streamErr != nil {
		// 检查是否为新的 HITL 中断
		if info, existed := compose.ExtractInterruptInfo(streamErr); existed {
			log.Printf("[SSE/Resume] 检测到新HITL中断(Stream返回): contexts=%d", len(info.InterruptContexts))
			h.streamHandleInterrupt(c, info, threadID)
			return
		}

		log.Printf("[SSE/Resume] Stream调用失败: %v", streamErr)
		errMsg := formatAgentError(streamErr.Error())
		emitter.Emit(StreamEvent{Type: EventTypeError, Content: errMsg})
		emitter.Emit(StreamEvent{Type: EventTypeDone})
		return
	}
	defer stream.Close()

	// 消费主输出流
	finalMessage, interruptInfo, recvErr := consumeStreamWithInterrupt(stream)

	// 如果 OnError 回调已经发射了 interrupt+done，直接返回
	emitter.mu.Lock()
	interruptFromCallback := emitter.sentInterrupt
	emitter.mu.Unlock()
	if interruptFromCallback {
		return
	}

	// 检查新的中断
	if interruptInfo != nil {
		log.Printf("[SSE/Resume] 检测到新HITL中断(Recv): contexts=%d", len(interruptInfo.InterruptContexts))
		h.streamHandleInterrupt(c, interruptInfo, threadID)
		return
	}

	// 检查接收错误
	if recvErr != nil {
		log.Printf("[SSE/Resume] 流接收错误: %v", recvErr)
		errMsg := formatAgentError(recvErr.Error())
		emitter.Emit(StreamEvent{Type: EventTypeError, Content: errMsg})
		emitter.Emit(StreamEvent{Type: EventTypeDone})
		return
	}

	// Fallback answer（与 ChatStream 相同逻辑）
	emitter.mu.Lock()
	sent := emitter.sentAnswer
	emitter.mu.Unlock()
	if !sent && finalMessage != nil && finalMessage.Content != "" {
		emitter.Emit(StreamEvent{Type: EventTypeAnswer, Content: finalMessage.Content})
	}

	// 等待 streaming goroutine 完成消息收集（5s 超时保护）
	done := make(chan struct{})
	go func() { collector.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		log.Printf("[SSE/Resume] Timeout waiting for message collector")
	}
	// 存储所有中间消息和最终回复
	allMessages := collector.Messages()
	for _, msg := range allMessages {
		_ = h.messageStore.Append(resumeCtx, threadID, msg)
	}
	if finalMessage != nil {
		_ = h.messageStore.Append(resumeCtx, threadID, finalMessage)
	}

	emitter.Emit(StreamEvent{Type: EventTypeDone})
}

// RebuildSupervisor 使用新的 ChatModel 重建 Supervisor
func (h *AgentHandler) RebuildSupervisor(
	ctx context.Context,
	chatModel einomodel.ToolCallingChatModel,
	toolLookup func(names []string) []tool.BaseTool,
	aclMiddleware compose.ToolMiddleware,
	hitlMiddleware compose.ToolMiddleware,
) error {
	_, specialists, err := BuildSpecialists(ctx, chatModel, h.agentDefs, toolLookup, aclMiddleware, hitlMiddleware)
	if err != nil {
		log.Printf("[Agent] 重建Specialists失败: %v", err)
		return err
	}

	newSupervisor, err := CreateSupervisor(ctx, chatModel, specialists)
	if err != nil {
		log.Printf("[Agent] 重建Supervisor失败: %v", err)
		return err
	}

	// 写锁保护 supervisor 交换
	h.mu.Lock()
	h.supervisor = newSupervisor
	h.mu.Unlock()

	log.Println("[Agent] Supervisor 重建成功")
	return nil
}

// SetMemoryExtractor 更新记忆提取器（LLM 配置变更时调用）
func (h *AgentHandler) SetMemoryExtractor(extractor memory.MemoryExtractor) {
	h.mu.Lock()
	h.memoryExtractor = extractor
	h.mu.Unlock()
}
