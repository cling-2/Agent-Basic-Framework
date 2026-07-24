package model

import (
	"context"
	"time"
)

// SessionStatus 会话状态枚举
type SessionStatus int

const (
	SessionActive  SessionStatus = 1 // 活跃
	SessionExpired SessionStatus = 2 // 已过期
	SessionRevoked SessionStatus = 3 // 已撤销（主动登出）
)

// Session 会话实体
// 设计说明：以 SessionID 为主键（UUID v4），无自增 ID；UpdatedAt 记录续期/撤销等变更时间
type Session struct {
	UserID    int64         `json:"user_id"`
	SessionID string        `json:"session_id"`
	Status    SessionStatus `json:"status"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	ExpiresAt time.Time     `json:"expires_at"`
}

// 过期策略常量
const (
	DefaultSessionTTL   = 30 * time.Minute // 默认 Session 有效期
	SessionRenewDelta   = 5  * time.Minute // 距过期不足此时间时自动续期
	MaxSessionLifetime  = 2  * time.Hour   // Session 最大生命周期（续期不超此上限）
	MaxSessionsPerUser  = 3                // 同一用户同时最多活跃 Session 数
	CleanupInterval     = 1  * time.Minute // 过期 Session 清理间隔
)

// UserContext 身份上下文，沿 Agent 调用链透传
// 设计原则：对 LLM 不可见（不出现在 prompt 中），对 Tool 和节点可见（通过 context.Context 传递）
type UserContext struct {
	UserID      int64        // 用户ID
	Role        string       // 角色名（admin / visitor）
	Permissions []Permission // 当前用户拥有的权限列表
	SessionID   string       // 会话标识
}

// permissionKey 用于从 context.Context 中存取 UserContext
type permissionKey struct{}

// WithUserContext 将 UserContext 注入 context.Context
func WithUserContext(ctx context.Context, uc *UserContext) context.Context {
	return context.WithValue(ctx, permissionKey{}, uc)
}

// UserContextFromCtx 从 context.Context 提取 UserContext
func UserContextFromCtx(ctx context.Context) (*UserContext, bool) {
	uc, ok := ctx.Value(permissionKey{}).(*UserContext)
	return uc, ok
}
