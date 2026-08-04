package context

import (
	stdctx "context"
	"log"
	"net/http"
	"strings"
	"sync"

	"kingsoft-agent/internal/hitl"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/gin-gonic/gin"

	pkgmodel "kingsoft-agent/pkg/model"
)

// ========== ContextManagerConfig ==========

// ContextManagerConfig 上下文管理配置
type ContextManagerConfig struct {
	// MaxMessages 最大保留消息数（0 = 不限制）
	// 仅用于快速短路判断，不作为独立裁剪阶段
	MaxMessages int

	// MaxTokens 最大保留 Token 数（0 = 不限制）
	// 代表 LLM 上下文窗口的 Token 上限
	MaxTokens int

	// SummaryThreshold 摘要触发阈值（Token 占比，如 0.8 表示 80%）
	// 当历史 Token 数超过 MaxTokens × SummaryThreshold 时触发摘要压缩
	SummaryThreshold float64

	// ChatModel 用于摘要压缩的 ChatModel（复用项目的 LLM 配置）
	// nil = 不启用摘要压缩
	ChatModel model.BaseChatModel
}

// DefaultConfig 返回默认配置
func DefaultConfig() ContextManagerConfig {
	return ContextManagerConfig{
		MaxMessages:      20,
		MaxTokens:        8000,
		SummaryThreshold: 0.8,
	}
}

// ========== ContextManager 接口 ==========

// ContextManager 上下文管理器接口
type ContextManager interface {
	// Process 对消息历史执行结构性保护 + 摘要压缩 + Token 裁剪 + 安全兜底
	// 五步执行顺序：STRUCTURAL_LOCK → 快速短路 → 摘要压缩 → TrimByToken → FINAL_GUARD
	// 注意：Process 输出仅用于本次 LLM 调用，不回写 MessageStore
	Process(ctx stdctx.Context, messages []*schema.Message) ([]*schema.Message, error)

	// SetConfig 更新配置（ChatModel 变化时重建摘要器）
	SetConfig(cfg ContextManagerConfig)
}

// ========== defaultContextManager 实现 ==========

// defaultContextManager 上下文管理器默认实现
type defaultContextManager struct {
	mu         sync.RWMutex
	config     ContextManagerConfig
	counter    TokenCounter
	summarizer ContextSummarizer // 可为 nil（不启用摘要）
}

// NewContextManager 创建上下文管理器
func NewContextManager(config ContextManagerConfig) ContextManager {
	counter := &DefaultTokenCounter{}

	var summarizer ContextSummarizer
	if config.ChatModel != nil && config.SummaryThreshold > 0 {
		summarizer = NewLLMContextSummarizer(config.ChatModel)
	}

	return &defaultContextManager{
		config:     config,
		counter:    counter,
		summarizer: summarizer,
	}
}

// Process 五步流程：STRUCTURAL_LOCK → 快速短路 → 摘要压缩 → TrimByToken → FINAL_GUARD
func (m *defaultContextManager) Process(ctx stdctx.Context, messages []*schema.Message) ([]*schema.Message, error) {
	if len(messages) == 0 {
		return messages, nil
	}

	// 快照配置，避免长时 LLM 调用持锁
	m.mu.RLock()
	config := m.config
	summarizer := m.summarizer
	counter := m.counter
	m.mu.RUnlock()

	result := make([]*schema.Message, len(messages))
	copy(result, messages)

	// ===== Step 1: STRUCTURAL_LOCK =====
	// 标记 system 消息位置 + ToolCall/ToolOutput 配对边界，定义"可操作区间"
	structure := analyzeMessageStructure(result)

	// ===== Step 2: 快速短路 =====
	// 消息数 ≤ maxMessages 且 Token ≤ maxTokens → 无需裁剪，直接返回
	totalTokens, err := counter.CountMessages(ctx, result)
	if err != nil {
		log.Printf("[Context] Token 计数失败: %v，降级使用原始消息", err)
		return result, nil // 降级：不裁剪
	}

	msgCountOK := config.MaxMessages <= 0 || len(structure.nonSystemGroups) <= config.MaxMessages
	tokenOK := config.MaxTokens <= 0 || totalTokens <= config.MaxTokens
	if msgCountOK && tokenOK {
		return result, nil // 快速短路：无需裁剪
	}

	// ===== Step 3: 摘要压缩 =====
	// Token 占用超过阈值时触发，摘要仅针对"可操作区间内的旧消息"
	if config.SummaryThreshold > 0 && config.MaxTokens > 0 && summarizer != nil {
		threshold := int(float64(config.MaxTokens) * config.SummaryThreshold)
		if totalTokens > threshold {
			summarized, summarizeErr := m.summarizeAndTrim(ctx, result, structure, config, counter, summarizer)
			if summarizeErr != nil {
				log.Printf("[Context] 摘要压缩失败: %v，跳过摘要，仅裁剪", summarizeErr)
			} else {
				result = summarized
			}
		}
	}

	// ===== Step 4: TrimByToken =====
	// 以完整消息对为最小丢弃单位
	if config.MaxTokens > 0 {
		currentTokens, countErr := counter.CountMessages(ctx, result)
		if countErr != nil {
			log.Printf("[Context] Token 计数失败: %v，跳过 Token 裁剪", countErr)
		} else if currentTokens > config.MaxTokens {
			result = TrimByToken(result, config.MaxTokens, counter)
		}
	}

	// ===== Step 5: FINAL_GUARD =====
	// 精确重计 Token，若仍超限则强制激进丢弃，确保不触发 LLM API 400
	if config.MaxTokens > 0 {
		result = m.finalGuard(ctx, result, config, counter)
	}

	return result, nil
}

// SetConfig 更新配置（ChatModel 变化时重建摘要器）
func (m *defaultContextManager) SetConfig(cfg ContextManagerConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.config = cfg

	// 重建摘要器
	if cfg.ChatModel != nil && cfg.SummaryThreshold > 0 {
		m.summarizer = NewLLMContextSummarizer(cfg.ChatModel)
	} else {
		m.summarizer = nil
	}
}

// summarizeAndTrim 摘要压缩：将可操作区间内的旧消息压缩为摘要，保留最近 K 轮原文不动
// 1. 利用 structure 标记的 system 位置和配对边界
// 2. 从尾部向前确定"保留区间"（最近的消息，Token 数不超过 maxTokens 的 50%）
// 3. 保留区间之前的旧消息（在可操作区间内）交给 Summarizer 压缩
// 4. 合并：system + 摘要 + 保留区间原文
func (m *defaultContextManager) summarizeAndTrim(
	ctx stdctx.Context,
	messages []*schema.Message,
	structure *messageStructure,
	config ContextManagerConfig,
	counter TokenCounter,
	summarizer ContextSummarizer,
) ([]*schema.Message, error) {
	// 分离 system 消息
	systemMsgs, nonSystemMsgs := separateSystemMessages(messages)

	// 从尾部向前确定"保留区间"（最近的消息，Token 数不超过 maxTokens 的 50%）
	retainTokenLimit := config.MaxTokens / 2
	retainStartIdx := len(nonSystemMsgs)
	accumulated := 0

	for i := len(nonSystemMsgs) - 1; i >= 0; i-- {
		count, _ := counter.CountMessage(ctx, nonSystemMsgs[i])
		accumulated += count
		if accumulated > retainTokenLimit {
			retainStartIdx = i + 1
			break
		}
	}

	// 旧消息交给摘要器
	oldMsgs := nonSystemMsgs[:retainStartIdx]
	if len(oldMsgs) == 0 {
		return messages, nil // 没有可摘要的旧消息
	}

	summaryMsg, err := summarizer.Summarize(ctx, oldMsgs)
	if err != nil {
		return nil, err
	}

	// 合并：system + 摘要 + 保留区间
	result := make([]*schema.Message, 0, len(systemMsgs)+1+(len(nonSystemMsgs)-retainStartIdx))
	result = append(result, systemMsgs...)
	result = append(result, summaryMsg)
	result = append(result, nonSystemMsgs[retainStartIdx:]...)

	return result, nil
}

// finalGuard 精确重计 Token，若仍超限则强制激进丢弃最旧消息对
// 这是防止 LLM API 返回 400 的最后防线
func (m *defaultContextManager) finalGuard(
	ctx stdctx.Context,
	messages []*schema.Message,
	config ContextManagerConfig,
	counter TokenCounter,
) []*schema.Message {
	const maxGuardIterations = 10 // 防止无限循环

	for i := 0; i < maxGuardIterations; i++ {
		currentTokens, err := counter.CountMessages(ctx, messages)
		if err != nil {
			log.Printf("[Context] FINAL_GUARD: Token 计数失败: %v，放弃校验", err)
			return messages
		}

		if currentTokens <= config.MaxTokens {
			return messages // 安全通过
		}

		// 仍超限 → 强制激进丢弃最旧的非 system 消息对
		newMessages := aggressiveDropOldest(messages)
		if len(newMessages) >= len(messages) {
			// 无法再丢弃任何消息（只剩 system 或至少保留一个组）
			log.Printf("[Context] FINAL_GUARD: 无法再丢弃任何消息，当前 Token=%d, 上限=%d",
				currentTokens, config.MaxTokens)
			return messages
		}
		messages = newMessages

		log.Printf("[Context] FINAL_GUARD: 第 %d 轮强制丢弃，当前 Token=%d, 上限=%d",
			i+1, currentTokens, config.MaxTokens)
	}

	return messages
}

// ========== ContextHandler HTTP 处理 ==========

// ContextStatsResponse 上下文统计响应
type ContextStatsResponse struct {
	ThreadID         string  `json:"thread_id"`
	MessageCount     int     `json:"message_count"`
	TokenCount       int     `json:"token_count"`
	MaxTokens        int     `json:"max_tokens"`
	MaxMessages      int     `json:"max_messages"`
	SummaryThreshold float64 `json:"summary_threshold"`
}

// ContextConfigRequest 上下文配置更新请求
type ContextConfigRequest struct {
	MaxMessages      int     `json:"max_messages"`
	MaxTokens        int     `json:"max_tokens"`
	SummaryThreshold float64 `json:"summary_threshold"`
}

// InterruptMeta 中断元数据（前端用于还原 HITL 审批卡片）
type InterruptMeta struct {
	InterruptID string `json:"interrupt_id"`
	ToolName    string `json:"tool_name"`
	ToolInput   string `json:"tool_input"`
	RiskReason  string `json:"risk_reason"`
}

// HistoryMessage 单条消息的历史记录（前端展示用）
type HistoryMessage struct {
	Role      string         `json:"role"`               // user / assistant
	Content   string         `json:"content"`            // 消息正文
	Interrupt *InterruptMeta `json:"interrupt,omitempty"` // HITL 中断元数据（仅中断消息携带）
}

// HistoryResponse 消息历史响应
type HistoryResponse struct {
	ThreadID  string           `json:"thread_id"`
	Messages  []HistoryMessage `json:"messages"`
}

// ContextHandler 上下文管理 HTTP 处理器
type ContextHandler struct {
	messageStore   MessageStore
	contextManager ContextManager
	counter        TokenCounter
	approvalStore  hitl.ApprovalStore // 用于 GetHistory 中断元数据丰富
}

// NewContextHandler 创建上下文管理 HTTP 处理器
func NewContextHandler(messageStore MessageStore, contextManager ContextManager, counter TokenCounter, approvalStore hitl.ApprovalStore) *ContextHandler {
	return &ContextHandler{
		messageStore:   messageStore,
		contextManager: contextManager,
		counter:        counter,
		approvalStore:  approvalStore,
	}
}

// GetStats 查询当前线程上下文统计（GET /api/context/stats?thread_id=xxx）
// 安全设计：验证线程所有权，用户只能查询自己的线程统计
func (h *ContextHandler) GetStats(c *gin.Context) {
	// 验证身份
	uc, ok := pkgmodel.UserContextFromCtx(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{
			"code": "UNAUTHORIZED", "message": "session invalid or expired",
		}})
		return
	}

	threadID := c.Query("thread_id")
	if threadID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code": "BAD_REQUEST", "message": "thread_id is required",
		}})
		return
	}

	// 数据隔离校验：非 admin 用户只能查询自己的线程
	if uc.Role != pkgmodel.RoleAdmin {
		owner, hasOwner := h.messageStore.GetOwner(c.Request.Context(), threadID)
		if hasOwner && owner != uc.UserID {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
				"code": "FORBIDDEN", "message": "you do not have access to this thread",
			}})
			return
		}
	}

	// 加载消息
	messages, err := h.messageStore.Get(c.Request.Context(), threadID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "INTERNAL_ERROR", "message": "failed to load messages",
		}})
		return
	}

	// 计算 Token 数
	tokenCount := 0
	if messages != nil {
		tokenCount, _ = h.counter.CountMessages(c.Request.Context(), messages)
	}

	// 读取配置快照
	mgr := h.contextManager.(*defaultContextManager)
	mgr.mu.RLock()
	config := mgr.config
	mgr.mu.RUnlock()

	c.JSON(http.StatusOK, ContextStatsResponse{
		ThreadID:         threadID,
		MessageCount:     len(messages),
		TokenCount:       tokenCount,
		MaxTokens:        config.MaxTokens,
		MaxMessages:      config.MaxMessages,
		SummaryThreshold: config.SummaryThreshold,
	})
}

// GetHistory 获取指定线程的消息历史
// GET /api/context/history?thread_id=xxx
// 安全设计：验证线程所有权，用户只能访问自己的对话历史
func (h *ContextHandler) GetHistory(c *gin.Context) {
	// 验证身份
	uc, ok := pkgmodel.UserContextFromCtx(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{
			"code": "UNAUTHORIZED", "message": "session invalid or expired",
		}})
		return
	}

	threadID := c.Query("thread_id")
	if threadID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code": "BAD_REQUEST", "message": "thread_id is required",
		}})
		return
	}

	// 数据隔离校验：非 admin 用户只能访问自己的线程
	if uc.Role != pkgmodel.RoleAdmin {
		owner, hasOwner := h.messageStore.GetOwner(c.Request.Context(), threadID)
		if hasOwner && owner != uc.UserID {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
				"code": "FORBIDDEN", "message": "you do not have access to this thread",
			}})
			return
		}
	}

	messages, err := h.messageStore.Get(c.Request.Context(), threadID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "INTERNAL_ERROR", "message": "failed to load messages",
		}})
		return
	}

	var raw []HistoryMessage
	for _, msg := range messages {
		if msg.Content == "" {
			continue // 跳过空内容消息（如纯 tool_call 消息）
		}
		role := string(msg.Role)
		// 跳过 tool 和 system 角色消息（不应出现在前端历史中，且会阻断 assistant 合并）
		if role == "tool" || role == "system" {
			continue
		}
		// 跳过系统内部 guidance 消息（[系统提示] 开头的 user 消息）
		// 这是 HITL Resume 流程的内部状态，不应展示给用户
		if role == "user" && strings.HasPrefix(msg.Content, "[系统提示]") {
			continue
		}
		raw = append(raw, HistoryMessage{
			Role:    role,
			Content: msg.Content,
		})
	}

	// 合并连续 assistant 消息：Supervisor 模式下 Host 路由文本和最终回答
	// 都作为独立 assistant 消息存储，只保留最后一个（最终回答）
	var history []HistoryMessage
	for _, msg := range raw {
		if len(history) > 0 && history[len(history)-1].Role == "assistant" && msg.Role == "assistant" {
			history[len(history)-1] = msg // 覆盖为最新的
		} else {
			history = append(history, msg)
		}
	}

	if history == nil {
		history = []HistoryMessage{}
	}


	// 从 ApprovalStore 丰富中断元数据：如果当前线程有待审批的中断，
	// 将 InterruptMeta 附加到最后一条 assistant 消息上，供前端恢复审批卡片
	if h.approvalStore != nil {
		if card := h.approvalStore.GetApproval(c.Request.Context(), threadID); card != nil {
			if len(history) > 0 && history[len(history)-1].Role == "assistant" {
				history[len(history)-1].Interrupt = &InterruptMeta{
					InterruptID: card.InterruptID,
					ToolName:    card.ApprovalInfo.ToolName,
					ToolInput:   card.ApprovalInfo.ToolInput,
					RiskReason:  card.ApprovalInfo.RiskReason,
				}
			}
		}
	}
	c.JSON(http.StatusOK, HistoryResponse{
		ThreadID: threadID,
		Messages: history,
	})
}

// UpdateConfig 更新上下文管理配置（POST /api/context/config，需 admin 角色）
func (h *ContextHandler) UpdateConfig(c *gin.Context) {
	// 验证身份 + admin 角色
	uc, ok := pkgmodel.UserContextFromCtx(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{
			"code": "UNAUTHORIZED", "message": "session invalid or expired",
		}})
		return
	}
	if uc.Role != pkgmodel.RoleAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"code": "FORBIDDEN", "message": "admin role required",
		}})
		return
	}

	var req ContextConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code": "BAD_REQUEST", "message": err.Error(),
		}})
		return
	}

	// 读取当前配置，合并更新
	mgr := h.contextManager.(*defaultContextManager)
	mgr.mu.RLock()
	currentCfg := mgr.config
	mgr.mu.RUnlock()

	// 合并：仅更新请求中提供的字段（0 值保留原配置）
	newCfg := currentCfg
	if req.MaxMessages > 0 {
		newCfg.MaxMessages = req.MaxMessages
	}
	if req.MaxTokens > 0 {
		newCfg.MaxTokens = req.MaxTokens
	}
	if req.SummaryThreshold > 0 {
		newCfg.SummaryThreshold = req.SummaryThreshold
	}

	h.contextManager.SetConfig(newCfg)

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}
