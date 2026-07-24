package api

import (
	"kingsoft-agent/internal/agent"
	"kingsoft-agent/internal/auth"
	ctxmgr "kingsoft-agent/internal/context"
	"kingsoft-agent/internal/settings"

	"github.com/gin-gonic/gin"
)

// SetupRoutes 注册所有 HTTP 路由
func SetupRoutes(
	r *gin.Engine,
	handler *auth.AuthHandler,
	authMiddleware gin.HandlerFunc,
	agentHandler *agent.AgentHandler,
	settingsHandler *settings.SettingsHandler,
	contextHandler *ctxmgr.ContextHandler,
) {
	// 公开接口（无需认证）
	authGroup := r.Group("/api/auth")
	{
		authGroup.POST("/login", handler.Login)
	}

	// 需要认证的接口
	authorized := r.Group("/api")
	authorized.Use(authMiddleware)
	{
		authorized.POST("/auth/logout", handler.Logout)
		authorized.GET("/auth/session", handler.GetSession)
		authorized.GET("/permissions/tools", handler.GetTools)

		// Agent 对话接口
		authorized.POST("/agent/chat", agentHandler.Chat)
		authorized.GET("/agent/chat/stream", agentHandler.ChatStream)
		// HITL 审批接口
		authorized.POST("/agent/checkpoint/:thread_id/decide", agentHandler.Resume)
		authorized.GET("/agent/checkpoint/:thread_id/decide/stream", agentHandler.ResumeStream)
		authorized.GET("/agent/checkpoints", agentHandler.ListCheckpoints)
		// 管理端接口（admin 权限在 handler 内校验）
		authorized.GET("/tools", agentHandler.ListTools)
		authorized.GET("/agents", agentHandler.ListAgents)

		// LLM 配置接口
		authorized.GET("/settings/status", settingsHandler.GetStatus)
		authorized.GET("/settings", settingsHandler.GetSettings)
		authorized.PUT("/settings", settingsHandler.UpdateSettings)
		authorized.POST("/settings/test", settingsHandler.TestConnection)

		// 上下文管理接口
		authorized.GET("/context/stats", contextHandler.GetStats)
		authorized.POST("/context/config", contextHandler.UpdateConfig)
	}
}
