package toolreg

import (
	"context"
	"fmt"

	"kingsoft-agent/internal/auth"
	"kingsoft-agent/pkg/model"

	"github.com/cloudwego/eino/compose"
)

// ACLToolMiddleware 创建 ACL 权限拦截中间件
// 基于 Eino ToolMiddleware 机制，在工具执行前统一拦截 ACL 检查
//
// 拦截逻辑：
//   - 从 context.Context 提取 UserContext
//   - 调用 ACLChecker.Allowed() 检查权限
//   - 无权限时回灌拒绝信息（不中断流程），Agent 可自主调整策略
//   - 有权限时放行，调用 next 继续执行
func ACLToolMiddleware(aclChecker auth.ACLChecker) compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				// 从 context.Context 提取 UserContext
				uc, ok := model.UserContextFromCtx(ctx)
				if !ok {
					return nil, fmt.Errorf("user context not found in request")
				}

				// ACL 检查
				allowed, err := aclChecker.Allowed(ctx, uc.Role, input.Name, "execute")
				if err != nil {
					return nil, fmt.Errorf("ACL check failed: %w", err)
				}

				if !allowed {
					// 回灌：返回拒绝信息，不抛异常中断流程
					denyMsg := fmt.Sprintf(
						"权限不足：当前角色[%s]无权调用工具[%s]。请使用其他方式完成任务或请求管理员授权。",
						uc.Role, input.Name,
					)
					return &compose.ToolOutput{Result: denyMsg}, nil
				}

				// 有权限，继续执行
				return next(ctx, input)
			}
		},
	}
}
