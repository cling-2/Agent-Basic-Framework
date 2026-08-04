package hitl

import (
	"context"
	"fmt"
	"time"

	"kingsoft-agent/pkg/model"

	"github.com/cloudwego/eino/compose"
)

// ---------- 审批决策 context 传递 ----------

// approvalDecisionKey 用于从 context.Context 中存取审批决策
type approvalDecisionKey struct{}

// WithApprovalDecision 将审批决策注入 context
func WithApprovalDecision(ctx context.Context, decision *ApprovalDecisionCtx) context.Context {
	return context.WithValue(ctx, approvalDecisionKey{}, decision)
}

// GetApprovalDecision 从 context 获取审批决策
func GetApprovalDecision(ctx context.Context) (*ApprovalDecisionCtx, bool) {
	d, ok := ctx.Value(approvalDecisionKey{}).(*ApprovalDecisionCtx)
	return d, ok
}

// threadIDKey 用于从 context.Context 中存取 threadID
type threadIDKey struct{}

// WithThreadID 将 threadID 注入 context
func WithThreadID(ctx context.Context, threadID string) context.Context {
	return context.WithValue(ctx, threadIDKey{}, threadID)
}

// GetThreadID 从 context 获取 threadID
func GetThreadID(ctx context.Context) string {
	if v, ok := ctx.Value(threadIDKey{}).(string); ok {
		return v
	}
	return ""
}

// originalMessageKey 用于从 context.Context 中存取原始用户消息
type originalMessageKey struct{}

// WithOriginalMessage 将原始用户消息注入 context
func WithOriginalMessage(ctx context.Context, msg string) context.Context {
	return context.WithValue(ctx, originalMessageKey{}, msg)
}

// GetOriginalMessage 从 context 获取原始用户消息
func GetOriginalMessage(ctx context.Context) string {
	if v, ok := ctx.Value(originalMessageKey{}).(string); ok {
		return v
	}
	return ""
}

// ---------- HumanApprovalMiddleware ----------

// HumanApprovalMiddleware 创建人工审批中间件
// 基于 Eino compose.StatefulInterrupt 发起中断，通过 GetApprovalDecision 获取恢复决策
//
// 中间件链顺序：ACLToolMiddleware → HumanApprovalMiddleware → Tool 执行
// 先过权限关再过审批关，ACL 不通过则不进入审批流程
func HumanApprovalMiddleware(riskChecker RiskChecker, approvalStore ApprovalStore) compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				// 1. 非高风险工具，直接放行
				if !riskChecker.IsHighRisk(input.Name) {
					return next(ctx, input)
				}

				// 2. 检查 context 中是否已有审批决策（恢复场景）
				if decision, ok := GetApprovalDecision(ctx); ok && decision.ToolName == input.Name {
					if decision.Decision == DecisionApprove {
						// 批准：执行工具
						return next(ctx, input)
					}
					// 拒绝：回灌拒绝信息（与 ACLToolMiddleware 回灌模式一致）
					reason := decision.Comment
					if reason == "" {
						reason = "用户拒绝了此操作"
					}
					return &compose.ToolOutput{
						Result: fmt.Sprintf("操作被拒绝：工具 %s 的人工审批未通过。原因：%s", input.Name, reason),
					}, nil
				}

				// 3. 无审批决策：高风险工具首次调用，发起中断
				threadID := GetThreadID(ctx)

				// 获取用户 ID 用于安全校验
				var userID int64
				if uc := getUserContext(ctx); uc != nil {
					userID = uc.UserID
				}

				approvalInfo := ApprovalInfo{
					ToolName:   input.Name,
					ToolInput:  input.Arguments,
					RiskReason: riskChecker.RiskReason(input.Name),
					CallID:     input.CallID,
					ThreadID:   threadID,
					UserID:     userID,
				}

				pendingState := PendingApprovalState{
					ToolName:  input.Name,
					ToolInput: input.Arguments,
					CallID:    input.CallID,
					ThreadID:  threadID,
				}

				// 保存审批卡片到 ApprovalStore
				// 使用 context.Background()，确保审批状态独立于请求 context 生命周期
				now := time.Now()
				card := &InterruptCard{
					InterruptID:     input.CallID,
					ApprovalInfo:    approvalInfo,
					OriginalMessage: GetOriginalMessage(ctx),
					CreatedAt:       now,
					ExpiresAt:       now.Add(DefaultApprovalTTL),
				}
				approvalStore.AddApproval(context.Background(), threadID, card)

				// 发起有状态中断——Eino 引擎拦截此 error 并传播到 handler
				return nil, compose.StatefulInterrupt(ctx, approvalInfo, pendingState)
			}
		},
	}
}

// getUserContext 从 context 获取 UserContext
func getUserContext(ctx context.Context) *model.UserContext {
	uc, _ := model.UserContextFromCtx(ctx)
	return uc
}
