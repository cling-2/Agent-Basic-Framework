package auth

import (
	"net/http"

	"kingsoft-agent/pkg/model"

	"github.com/gin-gonic/gin"
)

// AuthHandler 认证相关 HTTP 接口处理器
type AuthHandler struct {
	userStore    UserStore
	sessionStore SessionStore
	aclChecker   ACLChecker
}

// NewAuthHandler 创建认证处理器
func NewAuthHandler(userStore UserStore, sessionStore SessionStore, aclChecker ACLChecker) *AuthHandler {
	return &AuthHandler{
		userStore:    userStore,
		sessionStore: sessionStore,
		aclChecker:   aclChecker,
	}
}

// ---------- 请求/响应结构体 ----------

// LoginRequest 登录请求
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginResponse 登录响应
type LoginResponse struct {
	SessionID string `json:"session_id"`
	ExpiresIn int64  `json:"expires_in"` // 秒
}

// SessionInfoResponse 会话信息响应
type SessionInfoResponse struct {
	UserID    int64  `json:"user_id"`
	Role      string `json:"role"`
	ExpiresAt string `json:"expires_at"`
}

// ToolsResponse 可调用工具列表响应
type ToolsResponse struct {
	Tools []string `json:"tools"`
}

// MessageResponse 通用消息响应
type MessageResponse struct {
	Message string `json:"message"`
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

// ---------- 常用错误码 ----------

var (
	errUnauthorized   = ErrorDetail{Code: "UNAUTHORIZED", Message: "session invalid or expired"}
	errForbidden      = ErrorDetail{Code: "FORBIDDEN", Message: "insufficient permissions"}
	errBadRequest     = ErrorDetail{Code: "BAD_REQUEST", Message: "invalid request parameters"}
	errInvalidCreds   = ErrorDetail{Code: "UNAUTHORIZED", Message: "invalid credentials"}
	errUserDisabled   = ErrorDetail{Code: "UNAUTHORIZED", Message: "user account is disabled"}
)

// ---------- 处理器方法 ----------

// Login 用户登录
// POST /api/auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: errBadRequest})
		return
	}

	// 查询用户（不区分"不存在"和"密码错误"，统一返回 invalid credentials）
	user, err := h.userStore.GetByUsername(c.Request.Context(), req.Username)
	if err != nil {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: errInvalidCreds})
		return
	}

	// 校验密码
	if !checkPassword(req.Password, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: errInvalidCreds})
		return
	}

	// 检查用户状态
	if user.Status != model.UserEnabled {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: errUserDisabled})
		return
	}

	// 创建 Session
	session := NewSession(user.ID)
	if err := h.sessionStore.Create(c.Request.Context(), session); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: ErrorDetail{
			Code:    "INTERNAL_ERROR",
			Message: "failed to create session",
		}})
		return
	}

	c.JSON(http.StatusOK, LoginResponse{
		SessionID: session.SessionID,
		ExpiresIn: int64(model.DefaultSessionTTL.Seconds()),
	})
}

// Logout 用户登出
// POST /api/auth/logout
func (h *AuthHandler) Logout(c *gin.Context) {
	sessionID := extractBearerToken(c)
	if sessionID == "" {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: errUnauthorized})
		return
	}

	if err := h.sessionStore.Delete(c.Request.Context(), sessionID); err != nil {
		// 即使 session 不存在也返回成功（幂等性）
		c.JSON(http.StatusOK, MessageResponse{Message: "ok"})
		return
	}

	c.JSON(http.StatusOK, MessageResponse{Message: "ok"})
}

// GetSession 查询当前会话信息
// GET /api/auth/session
func (h *AuthHandler) GetSession(c *gin.Context) {
	uc, ok := model.UserContextFromCtx(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: errUnauthorized})
		return
	}

	// 获取会话以返回过期时间
	session, err := h.sessionStore.Get(c.Request.Context(), uc.SessionID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: errUnauthorized})
		return
	}

	c.JSON(http.StatusOK, SessionInfoResponse{
		UserID:    uc.UserID,
		Role:      uc.Role,
		ExpiresAt: session.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

// GetTools 查询当前用户可调用工具列表
// GET /api/permissions/tools
func (h *AuthHandler) GetTools(c *gin.Context) {
	uc, ok := model.UserContextFromCtx(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, ErrorResponse{Error: errUnauthorized})
		return
	}

	// 超级角色返回特殊标记
	if uc.Role == model.RoleAdmin {
		c.JSON(http.StatusOK, ToolsResponse{Tools: []string{"*"}})
		return
	}

	// 从 UserContext 的权限列表提取工具名（去重）
	toolSet := make(map[string]bool)
	for _, p := range uc.Permissions {
		toolSet[p.ToolName] = true
	}

	tools := make([]string, 0, len(toolSet))
	for t := range toolSet {
		tools = append(tools, t)
	}

	c.JSON(http.StatusOK, ToolsResponse{Tools: tools})
}

// extractBearerToken 从 Authorization 头提取 Bearer Token
func extractBearerToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if len(auth) > 7 && auth[:7] == "Bearer " {
		return auth[7:]
	}
	return ""
}
