package settings

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"kingsoft-agent/internal/agent"
	"kingsoft-agent/pkg/model"

	"github.com/cloudwego/eino/schema"
	"github.com/gin-gonic/gin"
)

// RebuildFunc 当配置变更时，重建 Supervisor 的回调函数
type RebuildFunc func(settings LLMSettings) error

// SettingsHandler 配置管理 HTTP 接口处理器
type SettingsHandler struct {
	store     *SettingsStore
	rebuildFn RebuildFunc
}

// NewSettingsHandler 创建配置处理器
func NewSettingsHandler(store *SettingsStore, rebuildFn RebuildFunc) *SettingsHandler {
	return &SettingsHandler{
		store:     store,
		rebuildFn: rebuildFn,
	}
}

// ---------- 请求/响应结构体 ----------

// UpdateSettingsRequest 更新配置请求
type UpdateSettingsRequest struct {
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
}

// SettingsResponse 配置响应
type SettingsResponse struct {
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
}

// SettingsStatusResponse 配置状态响应
type SettingsStatusResponse struct {
	Configured bool `json:"configured"`
}

// TestConnectionResponse 测试连接响应
type TestConnectionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ---------- 接口 ----------

// GetStatus 返回LLM配置状态（所有登录用户可访问）
func (h *SettingsHandler) GetStatus(c *gin.Context) {
	configured := h.store.IsConfigured()
	c.JSON(http.StatusOK, SettingsStatusResponse{Configured: configured})
}

// GetSettings 返回当前LLM配置（仅管理员）
func (h *SettingsHandler) GetSettings(c *gin.Context) {
	uc, ok := model.UserContextFromCtx(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权"})
		return
	}
	if uc.Role != model.RoleAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "仅管理员可访问"})
		return
	}

	s := h.store.Get()
	c.JSON(http.StatusOK, SettingsResponse{
		APIKey:  s.MaskedAPIKey(),
		BaseURL: s.BaseURL,
		Model:   s.Model,
	})
}

// TestConnection 测试LLM连接（仅管理员）
// POST /api/settings/test
func (h *SettingsHandler) TestConnection(c *gin.Context) {
	uc, ok := model.UserContextFromCtx(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权"})
		return
	}
	if uc.Role != model.RoleAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "仅管理员可操作"})
		return
	}

	var req UpdateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}

	// 处理脱敏API Key
	apiKey := req.APIKey
	if strings.Contains(apiKey, "****") {
		currentSettings := h.store.Get()
		apiKey = currentSettings.APIKey
	}

	// 如果API Key为空，直接返回Mock模式
	if apiKey == "" {
		c.JSON(http.StatusOK, TestConnectionResponse{
			Success: true,
			Message: "未配置API Key，将使用内置Mock模式（关键词路由，不调用真实LLM）",
		})
		return
	}

	// 尝试创建 ChatModel 并调用
	llmCfg := &agent.LLMConfig{
		APIKey:      apiKey,
		BaseURL:     req.BaseURL,
		Model:       req.Model,
		HeaderName:  "ksyun-code-type",
		HeaderValue: "kingsoft-agent",
	}

	chatModel, err := agent.NewChatModel(c.Request.Context(), llmCfg)
	if err != nil {
		c.JSON(http.StatusOK, TestConnectionResponse{
			Success: false,
			Message: fmt.Sprintf("创建ChatModel失败: %v", err),
		})
		return
	}

	// 发送简单测试请求（5秒超时）
	testCtx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	testMessages := []*schema.Message{
		schema.SystemMessage("你是一个助手，请用一句话回复。"),
		schema.UserMessage("你好"),
	}

	result, err := chatModel.Generate(testCtx, testMessages)
	if err != nil {
		log.Printf("[Settings] 测试连接失败: %v", err)
		errMsg := err.Error()
		// 提供友好错误提示
		if strings.Contains(errMsg, "connection refused") || strings.Contains(errMsg, "no such host") {
			errMsg = fmt.Sprintf("无法连接到 %s，请检查地址是否正确且服务可达", req.BaseURL)
		} else if strings.Contains(errMsg, "401") || strings.Contains(strings.ToLower(errMsg), "unauthorized") {
			errMsg = "API Key 认证失败，请检查 Key 是否正确"
		} else if strings.Contains(errMsg, "404") || strings.Contains(strings.ToLower(errMsg), "model") {
			errMsg = fmt.Sprintf("模型「%s」不存在，请检查模型名称", req.Model)
		}
		c.JSON(http.StatusOK, TestConnectionResponse{
			Success: false,
			Message: fmt.Sprintf("LLM连接测试失败: %s", errMsg),
		})
		return
	}

	reply := ""
	if result != nil {
		reply = result.Content
	}

	c.JSON(http.StatusOK, TestConnectionResponse{
		Success: true,
		Message: fmt.Sprintf("连接成功！模型回复: %s", truncate(reply, 100)),
	})
}

// UpdateSettings 更新LLM配置（仅管理员）
func (h *SettingsHandler) UpdateSettings(c *gin.Context) {
	uc, ok := model.UserContextFromCtx(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权"})
		return
	}
	if uc.Role != model.RoleAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "仅管理员可修改"})
		return
	}

	var req UpdateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}

	// 如果API Key包含****，说明是脱敏值，保留原有值
	currentSettings := h.store.Get()
	apiKey := req.APIKey
	if strings.Contains(apiKey, "****") {
		apiKey = currentSettings.APIKey
	}

	newSettings := LLMSettings{
		APIKey:  apiKey,
		BaseURL: req.BaseURL,
		Model:   req.Model,
	}

	// 保存配置到文件
	if err := h.store.Save(newSettings); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存配置失败: " + err.Error()})
		return
	}

	// 触发 Supervisor 重建
	if h.rebuildFn != nil {
		if err := h.rebuildFn(newSettings); err != nil {
			log.Printf("[Settings] 重建Supervisor失败: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "重建Agent失败: " + err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    "配置已保存",
		"configured": newSettings.Configured(),
	})
}

// truncate 截断字符串
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
