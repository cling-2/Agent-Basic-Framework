package auth

import (
	"net/http"

	"kingsoft-agent/pkg/model"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware 认证中间件
// 从 Authorization: Bearer {sessionId} 头提取 sessionId，
// 校验会话合法性，构建 UserContext 并注入请求上下文
func AuthMiddleware(sessionStore SessionStore, userStore UserStore, aclChecker ACLChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID := extractBearerToken(c)
		if sessionID == "" {
			c.JSON(http.StatusUnauthorized, ErrorResponse{Error: errUnauthorized})
			c.Abort()
			return
		}

		// 校验 Session 合法性
		session, err := sessionStore.Get(c.Request.Context(), sessionID)
		if err != nil {
			c.JSON(http.StatusUnauthorized, ErrorResponse{Error: errUnauthorized})
			c.Abort()
			return
		}

		// 查询用户信息
		user, err := userStore.GetByID(c.Request.Context(), session.UserID)
		if err != nil {
			c.JSON(http.StatusUnauthorized, ErrorResponse{Error: errUnauthorized})
			c.Abort()
			return
		}

		// 检查用户状态
		if user.Status != model.UserEnabled {
			c.JSON(http.StatusUnauthorized, ErrorResponse{Error: errUserDisabled})
			c.Abort()
			return
		}

		// 获取用户权限
		perms, _ := aclChecker.PermissionsForRole(c.Request.Context(), user.RoleName)

		// 构建 UserContext 并注入
		uc := &model.UserContext{
			UserID:      user.ID,
			Role:        user.RoleName,
			Permissions: perms,
			SessionID:   session.SessionID,
		}
		ctx := model.WithUserContext(c.Request.Context(), uc)
		c.Request = c.Request.WithContext(ctx)

		// 滑动续期：距过期不足 RenewDelta 时自动续期
		if session.ExpiresAt.Sub(timeNow()) < model.SessionRenewDelta {
			_ = sessionStore.Renew(c.Request.Context(), sessionID) // 续期失败不影响本次请求
		}

		c.Next()
	}
}
