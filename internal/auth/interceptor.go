package auth

import (
	"context"
	"fmt"

	"kingsoft-agent/pkg/model"
)

// ToolCall 工具调用请求（与 Eino 框架对接时替换为实际类型）
type ToolCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

// ToolResult 工具调用结果（与 Eino 框架对接时替换为实际类型）
type ToolResult struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// // ToolInterceptor 工具调用拦截器
// // 作为 Eino Graph 的 Callback 注入，在 Tool 执行前统一拦截
// //
// // 返回值含义：
// //   - (nil, nil)    → 放行，继续执行 Tool
// //   - (*ToolResult, nil) → 拦截，回灌拒绝信息给 Agent（不中断流程）
// //   - (nil, error)  → 系统错误，中断流程
// func ToolInterceptor(acl ACLChecker) func(ctx context.Context, toolCall *ToolCall) (*ToolResult, error) {
// 	return func(ctx context.Context, toolCall *ToolCall) (*ToolResult, error) {
// 		uc, ok := model.UserContextFromCtx(ctx)
// 		if !ok {
// 			return nil, fmt.Errorf("user context not found in request")
// 		}

// 		allowed, err := acl.Allowed(ctx, uc.Role, toolCall.Name, "execute")
// 		if err != nil {
// 			return nil, fmt.Errorf("ACL check failed: %w", err)
// 		}

// 		if !allowed {
// 			// 回灌：不抛异常中断，而是返回结构化拒绝信息
// 			return &ToolResult{
// 				Content: fmt.Sprintf(
// 					"权限不足：当前角色[%s]无权调用工具[%s]。请使用其他方式完成任务或请求管理员授权。",
// 					uc.Role, toolCall.Name,
// 				),
// 				IsError: true,
// 			}, nil
// 		}

// 		// 有权限，继续执行
// 		return nil, nil
// 	}
// }
