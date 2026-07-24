package hitl

import (
	"time"

	"github.com/cloudwego/eino/schema"
)

// 审批决策常量
const (
	DecisionApprove = "approve"
	DecisionReject  = "reject"

	// DefaultApprovalTTL 默认审批有效期
	DefaultApprovalTTL = 30 * time.Minute
	// CleanupInterval 过期清理间隔
	CleanupInterval = 5 * time.Minute
)

// ApprovalInfo 面向用户的审批信息
// 通过 compose.StatefulInterrupt 的 info 参数传递，由 handler 提取后返回给前端
type ApprovalInfo struct {
	ToolName   string `json:"tool_name"`   // 待审批工具名
	ToolInput  string `json:"tool_input"`  // 工具参数 JSON
	RiskReason string `json:"risk_reason"` // 高风险原因
	CallID     string `json:"call_id"`     // Eino ToolCall ID
	ThreadID   string `json:"thread_id"`   // 会话线程 ID
	UserID     int64  `json:"user_id"`     // 发起用户 ID（安全校验用）
}

// PendingApprovalState 审批等待时的内部状态
// 通过 compose.StatefulInterrupt 的 state 参数持久化
type PendingApprovalState struct {
	ToolName  string `json:"tool_name"`
	ToolInput string `json:"tool_input"`
	CallID    string `json:"call_id"`
	ThreadID  string `json:"thread_id"`
}

// ApprovalDecisionCtx 审批决策上下文
// 恢复时由 handler 注入 context，中间件读取后放行或拒绝
type ApprovalDecisionCtx struct {
	ThreadID string // 会话线程 ID
	ToolName string // 目标工具名
	Decision string // approve / reject
	Comment  string // 拒绝原因
}

// InterruptCard 待审批检查点卡片
// 存储在 ApprovalStore 中，供 API 查询和前端展示
type InterruptCard struct {
	InterruptID    string        `json:"interrupt_id"`    // 中断点 ID
	ApprovalInfo   ApprovalInfo  `json:"approval_info"`   // 审批信息
	OriginalMessage string       `json:"original_message"` // 触发中断的用户原始消息
	CreatedAt      time.Time     `json:"created_at"`      // 创建时间
	ExpiresAt      time.Time     `json:"expires_at"`      // 过期时间
}

// IsExpired 检查审批是否已过期
func (c *InterruptCard) IsExpired() bool {
	return time.Now().After(c.ExpiresAt)
}

// InterruptInfo 中断响应信息（ChatResponse 扩展字段）
type InterruptInfo struct {
	InterruptID string `json:"interrupt_id"` // 中断点 ID
	ToolName    string `json:"tool_name"`    // 待审批工具名
	ToolInput   string `json:"tool_input"`   // 工具参数 JSON
	RiskReason  string `json:"risk_reason"`  // 高风险原因
}

func init() {
	// 注册 HITL 类型以支持 Eino Checkpoint gob 序列化
	schema.RegisterName[ApprovalInfo]("hitl.ApprovalInfo")
	schema.RegisterName[PendingApprovalState]("hitl.PendingApprovalState")
	schema.RegisterName[ApprovalDecisionCtx]("hitl.ApprovalDecisionCtx")
}
