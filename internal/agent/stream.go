package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"kingsoft-agent/internal/hitl"
	pkgmodel "kingsoft-agent/pkg/model"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	toolcallback "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	flowagent "github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/schema"

	"github.com/gin-gonic/gin"
)

// ---------- SSE 事件类型 ----------

type StreamEventType string

const (
	EventTypeThinking   StreamEventType = "thinking"
	EventTypeToolCall   StreamEventType = "tool_call"
	EventTypeToolResult StreamEventType = "tool_result"
	EventTypeRouting    StreamEventType = "routing"
	EventTypeAnswer     StreamEventType = "answer"
	EventTypeInterrupt  StreamEventType = "interrupt"
	EventTypeDone       StreamEventType = "done"
	EventTypeError      StreamEventType = "error"
)

type StreamEvent struct {
	Type      StreamEventType     `json:"type"`
	Content   string              `json:"content,omitempty"`
	Tool      *ToolCallInfo       `json:"tool,omitempty"`
	Interrupt *hitl.InterruptInfo `json:"interrupt,omitempty"`
}

type ToolCallInfo struct {
	Name string `json:"name"`
	Args string `json:"args"`
	ID   string `json:"id"`
}

// ---------- SSE 写入辅助 ----------

func writeSSE(c *gin.Context, event StreamEvent) bool {
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("[SSE] JSON marshal error: %v", err)
		return false
	}
	_, err = fmt.Fprintf(c.Writer, "data: %s\n\n", data)
	if err != nil {
		return false
	}
	c.Writer.(http.Flusher).Flush()
	return true
}

// ---------- SSE 事件发射器 ----------

// SSEEmitter 将 Eino Callback 事件转换为 SSE 事件写入 HTTP 响应
//
// 核心 answer 过滤策略（基于状态机而非 depth）：
//   - 未路由到 Specialist（routedToSpecialist=false）：Host 直接回答 → 发射 answer
//   - 已路由但 Specialist 未完成（specialistCompleted=false）：路由文本 → 不发射 answer
//   - Specialist 已完成（specialistCompleted=true）：Host/Summarizer 最终答案 → 发射 answer
//
// 这避免了 depth-based 过滤的根本缺陷：depth 跟踪依赖 OnStart/OnEnd 的精确时序，
// 而 Eino 回调的嵌套传播顺序可能不符合预期。状态机方式只在关键的 Specialist
// OnStart/OnEnd 边界点切换状态，不依赖 depth 的精确时序。
type SSEEmitter struct {
	c               *gin.Context
	threadID        string
	sentAnswer      bool           // 是否已发射过 Host 层面的 answer
	sentThinking    bool           // 是否已发射过 thinking（整个会话只一次）
	sentInterrupt   bool           // 是否已从 OnError 中发射过 interrupt（抑制后续事件）
	routedToSpecialist bool        // Specialist-as-tool OnStart 已触发
	specialistCompleted bool       // Specialist-as-tool OnEnd 已触发
	specialistNames map[string]bool // Specialist 名称集合
	emittedCalls    map[string]bool // ChatModel 流中已见过的 ToolCall ID（用于去重）
	calledToolNames map[string]bool // 实际被调用的工具名集合（OnStart 时记录，用于意图兜底）
	mu              sync.Mutex
}

func newSSEEmitter(c *gin.Context, threadID string, specialistNames map[string]bool) *SSEEmitter {
	return &SSEEmitter{
		c:               c,
		threadID:        threadID,
		specialistNames: specialistNames,
		emittedCalls:    make(map[string]bool),
		calledToolNames: make(map[string]bool),
	}
}

func (e *SSEEmitter) Emit(event StreamEvent) {
	writeSSE(e.c, event)
}

func (e *SSEEmitter) isSpecialist(name string) bool {
	return e.specialistNames[name]
}

// BuildCallback 构建 Eino Callback Handler
func (e *SSEEmitter) BuildCallback() callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		// OnStart: ChatModel 开始推理 / Tool 开始执行
		OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
			e.mu.Lock()
			defer e.mu.Unlock()

			if e.sentInterrupt {
				return ctx
			}

			if info.Component == components.ComponentOfChatModel {
				if !e.sentThinking {
					e.sentThinking = true
					e.Emit(StreamEvent{Type: EventTypeThinking, Content: "思考中..."})
				}
			}
			if info.Component == components.ComponentOfTool {
				toolInput := toolcallback.ConvCallbackInput(input)
				if toolInput != nil {
					if e.isSpecialist(info.Name) {
						e.routedToSpecialist = true
						e.specialistCompleted = false // 多轮路由时重置
						e.Emit(StreamEvent{
							Type:    EventTypeRouting,
							Content: fmt.Sprintf("路由到 %s...", info.Name),
							Tool:    &ToolCallInfo{Name: info.Name},
						})
					} else {
						// 实际工具调用
						e.calledToolNames[info.Name] = true
						// 去重：如果 ChatModel 流中已标记过这个 tool call，跳过
						callKey := info.Name
						if e.emittedCalls[callKey] {
							// ChatModel 流已预告，OnStart 不再重复发射
						} else {
							e.Emit(StreamEvent{
								Type:    EventTypeToolCall,
								Tool:    &ToolCallInfo{Name: info.Name, Args: toolInput.ArgumentsInJSON},
								Content: fmt.Sprintf("正在调用 %s...", info.Name),
							})
						}
					}
				}
			}
			return ctx
		}).
		// OnEnd: Tool 执行完成
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
			e.mu.Lock()
			defer e.mu.Unlock()

			if e.sentInterrupt {
				return ctx
			}

			if info.Component == components.ComponentOfTool {
				if e.isSpecialist(info.Name) {
					e.specialistCompleted = true
					return ctx
				}
				// 实际工具完成 → 发射 tool_result
				toolOutput := toolcallback.ConvCallbackOutput(output)
				if toolOutput != nil {
					summary := extractToolSummary(toolOutput.Response)
					e.Emit(StreamEvent{Type: EventTypeToolResult, Content: summary})
				}
			}
			return ctx
		}).
		// OnEndWithStreamOutput: ChatModel 流式输出
		// 核心变更：不在流中发射 tool_call/routing 事件（由 OnStart 处理）
		// answer 事件根据状态机决定是否发射
		OnEndWithStreamOutputFn(func(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
			if info.Component != components.ComponentOfChatModel {
				output.Close()
				return ctx
			}

			defer output.Close()
			for {
				chunk, err := output.Recv()
				if err != nil {
					if err != io.EOF {
						log.Printf("[SSE] ChatModel stream recv error: %v", err)
					}
					break
				}

				modelOut, ok := chunk.(*model.CallbackOutput)
				if !ok || modelOut == nil || modelOut.Message == nil {
					continue
				}

				msg := modelOut.Message

				e.mu.Lock()
				if e.sentInterrupt {
					e.mu.Unlock()
					continue
				}

				// ToolCall 处理：仅标记 emittedCalls，不发射事件
				// 事件统一由 Tool OnStart 发射，避免与 OnStart 重复
				if len(msg.ToolCalls) > 0 {
					for _, tc := range msg.ToolCalls {
						if tc.Function.Name == "" {
							continue // 流式增量 chunk
						}
						callKey := tc.ID
						if callKey == "" {
							callKey = tc.Function.Name
						}
						e.emittedCalls[callKey] = true
					}
				}

				// Content → answer 事件（根据状态机决定是否发射）
				if msg.Content != "" {
					shouldEmit := false

					if !e.routedToSpecialist {
						// 未路由到 Specialist：Host 直接回答，发射
						shouldEmit = true
					} else if e.specialistCompleted {
						// Specialist 已完成：Host/Summarizer 最终答案，发射
						shouldEmit = true
					}
					// routedToSpecialist && !specialistCompleted：路由文本，不发射

					if shouldEmit {
						e.sentAnswer = true
						e.Emit(StreamEvent{Type: EventTypeAnswer, Content: msg.Content})
					}
				}

				e.mu.Unlock()
			}

			return ctx
		}).
		// OnError: 检测 HITL 中断错误
		// compose.StatefulInterrupt 可能被 ReAct Agent 内部消化而不传播到 Stream handler，
		// 因此在 OnError 回调层面直接捕获并发射 interrupt 事件
		OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			e.mu.Lock()
			defer e.mu.Unlock()

			if interruptInfo, existed := compose.ExtractInterruptInfo(err); existed {
				if !e.sentInterrupt {
					e.sentInterrupt = true
					for _, ic := range interruptInfo.InterruptContexts {
						approvalInfo, ok := ic.Info.(hitl.ApprovalInfo)
						if !ok {
							continue
						}
						e.Emit(StreamEvent{
							Type:    EventTypeInterrupt,
							Content: "⏸️ 操作需要人工审批，请在审批面板中确认。",
							Interrupt: &hitl.InterruptInfo{
								InterruptID: ic.ID,
								ToolName:    approvalInfo.ToolName,
								ToolInput:   approvalInfo.ToolInput,
								RiskReason:  approvalInfo.RiskReason,
							},
						})
					}
					e.Emit(StreamEvent{Type: EventTypeDone})
				}
				return ctx
			}

			log.Printf("[SSE:OnError] component=%s name=%s error=%v", info.Component, info.Name, err)
			return ctx
		}).
		Build()
}

// ---------- ChatStream 流式对话 ----------

func (h *AgentHandler) ChatStream(c *gin.Context) {
	message := c.Query("message")
	threadID := c.Query("thread_id")
	if message == "" || threadID == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: errBadRequest})
		return
	}

	uc, ok := pkgmodel.UserContextFromCtx(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: ErrorDetail{
			Code: "UNAUTHORIZED", Message: "session invalid or expired",
		}})
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.(http.Flusher).Flush()

	emitter := newSSEEmitter(c, threadID, h.specialistNameSet())
	cb := emitter.BuildCallback()

	ctx, cancel := context.WithTimeout(context.Background(), AgentTimeout)
	defer cancel()

	ctx = hitl.WithThreadID(ctx, threadID)
	ctx = hitl.WithOriginalMessage(ctx, message)
	ctx = pkgmodel.WithUserContext(ctx, uc)

	// 注入 MessageCollector（供 Specialist 包装器捕获 ToolCall/ToolResult 中间消息）
	collector := NewMessageCollector()
	ctx = WithMessageCollector(ctx, collector)

	opts := []flowagent.AgentOption{
		flowagent.WithComposeOptions(compose.WithCallbacks(cb)),
	}

	history, histErr := h.messageStore.Get(ctx, threadID)
	if histErr != nil {
		log.Printf("[Context/SSE] 加载历史失败: %v", histErr)
		history = nil
	}
	userMsg := schema.UserMessage(message)
	_ = h.messageStore.Append(ctx, threadID, userMsg)

	var fullMessages []*schema.Message
	if history != nil {
		fullMessages = append(history, userMsg)
	} else {
		fullMessages = []*schema.Message{userMsg}
	}

	trimmedPrompt, processErr := h.contextManager.Process(ctx, fullMessages)
	if processErr != nil {
		log.Printf("[Context/SSE] 上下文处理失败: %v，降级使用原始消息", processErr)
		trimmedPrompt = fullMessages
	}

	h.mu.RLock()
	supervisor := h.supervisor
	h.mu.RUnlock()

	stream, streamErr := supervisor.Stream(ctx, trimmedPrompt, opts...)
	if streamErr != nil {
		if info, existed := compose.ExtractInterruptInfo(streamErr); existed {
			log.Printf("[SSE] 检测到HITL中断(Stream返回): contexts=%d", len(info.InterruptContexts))
			h.streamHandleInterrupt(c, info, threadID)
			return
		}

		log.Printf("[SSE] Stream调用失败: %v", streamErr)
		errMsg := formatAgentError(streamErr.Error())
		emitter.Emit(StreamEvent{Type: EventTypeError, Content: errMsg})
		emitter.Emit(StreamEvent{Type: EventTypeDone})
		return
	}
	defer stream.Close()

	finalMessage, interruptInfo, recvErr := consumeStreamWithInterrupt(stream)

	// 检查 OnError 回调是否已经发射了 interrupt+done
	emitter.mu.Lock()
	interruptFromCallback := emitter.sentInterrupt
	emitter.mu.Unlock()
	if interruptFromCallback {
		return
	}

	if interruptInfo != nil {
		log.Printf("[SSE] 检测到HITL中断(Recv): contexts=%d", len(interruptInfo.InterruptContexts))
		h.streamHandleInterrupt(c, interruptInfo, threadID)
		return
	}

	if recvErr != nil {
		log.Printf("[SSE] 流接收错误: %v", recvErr)
		errMsg := formatAgentError(recvErr.Error())
		emitter.Emit(StreamEvent{Type: EventTypeError, Content: errMsg})
		emitter.Emit(StreamEvent{Type: EventTypeDone})
		return
	}

	// 意图兜底检查：如果用户消息匹配高风险意图，但 LLM 未调用对应工具且无中断，
	// 则基于意图强制触发 HITL 中断（确保高风险操作100%需要审批）
	emitter.mu.Lock()
	intentInterrupt := h.checkIntentRisk(ctx, message, emitter.calledToolNames, emitter.sentInterrupt)
	emitter.mu.Unlock()
	if intentInterrupt != nil {
		// 将意图中断也存入 ApprovalStore，以便 ResumeStream 统一处理
		approvalInfo := hitl.ApprovalInfo{
			ToolName:  intentInterrupt.ToolName,
			ToolInput: intentInterrupt.ToolInput,
			RiskReason: intentInterrupt.RiskReason,
			CallID:    intentInterrupt.InterruptID,
			ThreadID:  threadID,
			UserID:    uc.UserID,
		}
		card := &hitl.InterruptCard{
			InterruptID:    intentInterrupt.InterruptID,
			ApprovalInfo:   approvalInfo,
			OriginalMessage: message,
			CreatedAt:      time.Now(),
			ExpiresAt:      time.Now().Add(hitl.DefaultApprovalTTL),
		}
		bgCtx := context.Background()
		h.approvalStore.AddApproval(bgCtx, threadID, card)

		emitter.Emit(StreamEvent{
			Type:    EventTypeInterrupt,
			Content: "⏸️ 操作需要人工审批，请在审批面板中确认。",
			Interrupt: intentInterrupt,
		})
		// 等待 streaming goroutine 完成消息收集（5s 超时保护）
		done := make(chan struct{})
		go func() { collector.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			log.Printf("[SSE] Timeout waiting for message collector")
		}
		// 存储所有中间消息和最终回复
		allMessages := collector.Messages()
		for _, msg := range allMessages {
			_ = h.messageStore.Append(ctx, threadID, msg)
		}
		if finalMessage != nil {
			_ = h.messageStore.Append(ctx, threadID, finalMessage)
		}
		emitter.Emit(StreamEvent{Type: EventTypeDone})
		return
	}

	// Fallback answer：如果 Host 层从未发射 answer，从主输出流最终消息补充
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
		log.Printf("[SSE] Timeout waiting for message collector")
	}
	// 存储所有中间消息和最终回复
	allMessages := collector.Messages()
	for _, msg := range allMessages {
		_ = h.messageStore.Append(ctx, threadID, msg)
	}
	if finalMessage != nil {
		_ = h.messageStore.Append(ctx, threadID, finalMessage)
	}

	emitter.Emit(StreamEvent{Type: EventTypeDone})
}

func consumeStreamWithInterrupt(stream *schema.StreamReader[*schema.Message]) (*schema.Message, *compose.InterruptInfo, error) {
	copies := stream.Copy(2)
	sseStream := copies[0]
	storeStream := copies[1]

	// 消费 SSE stream 进行中断检测
	var lastErr error
	for {
		_, recvErr := sseStream.Recv()
		if recvErr != nil {
			lastErr = recvErr
			break
		}
	}

	// 检查中断信息
	var interruptInfo *compose.InterruptInfo
	if lastErr != nil && lastErr != io.EOF {
		if info, ok := compose.ExtractInterruptInfo(lastErr); ok {
			interruptInfo = info
		}
	}

	// 合并 store stream 为完整消息（用于存储）
	finalMessage, concatErr := schema.ConcatMessageStream(storeStream)
	if concatErr != nil {
		log.Printf("[Stream] 合并流失败: %v", concatErr)
	}

	// 非中断错误返回
	if lastErr != nil && lastErr != io.EOF && interruptInfo == nil {
		return finalMessage, nil, lastErr
	}

	return finalMessage, interruptInfo, nil
}

func (h *AgentHandler) streamHandleInterrupt(c *gin.Context, info *compose.InterruptInfo, threadID string) {
	for _, ic := range info.InterruptContexts {
		approvalInfo, ok := ic.Info.(hitl.ApprovalInfo)
		if !ok {
			continue
		}
		writeSSE(c, StreamEvent{
			Type:    EventTypeInterrupt,
			Content: "⏸️ 操作需要人工审批，请在审批面板中确认。",
			Interrupt: &hitl.InterruptInfo{
				InterruptID: ic.ID,
				ToolName:    approvalInfo.ToolName,
				ToolInput:   approvalInfo.ToolInput,
				RiskReason:  approvalInfo.RiskReason,
			},
		})
		writeSSE(c, StreamEvent{Type: EventTypeDone})
		return
	}

	writeSSE(c, StreamEvent{Type: EventTypeError, Content: "agent execution interrupted"})
	writeSSE(c, StreamEvent{Type: EventTypeDone})
}

func extractToolSummary(toolOutput string) string {
	if toolOutput == "" {
		return "工具执行完成"
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(toolOutput), &obj); err != nil {
		if len(toolOutput) > 200 {
			return toolOutput[:200] + "..."
		}
		return toolOutput
	}
	if summary, ok := obj["summary"].(string); ok && summary != "" {
		return summary
	}
	content := toolOutput
	if len(content) > 200 {
		content = content[:200] + "..."
	}
	return content
}

// ---------- 意图风险兜底检查 ----------

// IntentRiskChecker 意图风险检查器接口
// 分析用户消息意图，判断是否匹配高风险操作模式
type IntentRiskChecker interface {
	// CheckIntentRisk 检查用户消息是否匹配高风险意图
	// 返回匹配到的工具名、风险原因，如果不匹配返回空
	CheckIntentRisk(message string) (toolName string, riskReason string, matched bool)
}

// MemoryIntentRiskChecker 基于关键词的意图风险检查器
type MemoryIntentRiskChecker struct {
	// 关键词 -> (匹配的工具名, 风险原因)
	patterns []IntentPattern
}

// IntentPattern 意图匹配模式
type IntentPattern struct {
	Keywords []string // 匹配关键词（任意一个匹配即命中）
	ToolName string   // 关联的高风险工具名
	RiskReason string // 风险原因描述
}

func NewMemoryIntentRiskChecker() *MemoryIntentRiskChecker {
	return &MemoryIntentRiskChecker{}
}

func (m *MemoryIntentRiskChecker) AddPattern(keywords []string, toolName string, riskReason string) {
	m.patterns = append(m.patterns, IntentPattern{
		Keywords:   keywords,
		ToolName:   toolName,
		RiskReason: riskReason,
	})
}

func (m *MemoryIntentRiskChecker) CheckIntentRisk(message string) (string, string, bool) {
	lowerMsg := strings.ToLower(message)
	for _, p := range m.patterns {
		for _, kw := range p.Keywords {
			if strings.Contains(lowerMsg, strings.ToLower(kw)) {
				return p.ToolName, p.RiskReason, true
			}
		}
	}
	return "", "", false
}

// checkIntentRisk 后置意图兜底检查
// 条件：用户消息匹配高风险意图 + LLM 未调用对应工具 + 无已有中断 + 用户有 ACL 权限
// 效果：基于意图强制触发 HITL 中断，确保高风险操作100%需要审批
func (h *AgentHandler) checkIntentRisk(ctx context.Context, message string, calledToolNames map[string]bool, alreadyInterrupted bool) *hitl.InterruptInfo {
	if alreadyInterrupted || h.intentRiskChecker == nil {
		return nil
	}

	toolName, riskReason, matched := h.intentRiskChecker.CheckIntentRisk(message)
	if !matched {
		return nil
	}

	// 如果高风险工具已被实际调用（OnStart 有记录），说明 HITL 已正常触发
	if calledToolNames[toolName] {
		return nil
	}

	// ACL 门控：如果用户无权调用该工具，不触发意图兜底中断
	// 此场景下 ACL 中间件已在工具调用路径中拒绝了请求，无需再触发 HITL
	uc, ok := pkgmodel.UserContextFromCtx(ctx)
	if !ok {
		log.Printf("[SSE/Intent] 无法获取UserContext，跳过意图兜底中断")
		return nil
	}
	allowed, aclErr := h.aclChecker.Allowed(ctx, uc.Role, toolName, "execute")
	if aclErr != nil {
		log.Printf("[SSE/Intent] ACL检查失败: %v，跳过意图兜底中断", aclErr)
		return nil
	}
	if !allowed {
		log.Printf("[SSE/Intent] 用户角色[%s]无权调用工具[%s]，ACL已在中间件路径拒绝，跳过意图兜底", uc.Role, toolName)
		return nil
	}

	// 用户有权限 + LLM 未调用高风险工具 + 意图匹配 → 基于意图强制中断
	log.Printf("[SSE/Intent] 意图兜底触发: message=%q matchedTool=%s risk=%s", message, toolName, riskReason)

	return &hitl.InterruptInfo{
		InterruptID: fmt.Sprintf("intent_%s_%d", toolName, time.Now().UnixNano()),
		ToolName:    toolName,
		ToolInput:   message,
		RiskReason:  riskReason,
	}
}
