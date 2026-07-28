package memory

import (
	"context"
	"net/http"

	pkgmodel "kingsoft-agent/pkg/model"

	"github.com/gin-gonic/gin"
)

// ========== 请求/响应结构体 ==========

// MemoryPutRequest 写入长期记忆请求
type MemoryPutRequest struct {
	Key      string `json:"key" binding:"required"`   // 条目键（如 "preference_language"）
	Value    string `json:"value" binding:"required"` // 条目值（如 "Python"）
	Category string `json:"category"`                 // 分类（preference / fact / rule），默认 "fact"
}

// MemoryListResponse 长期记忆列表响应
type MemoryListResponse struct {
	Entries []*MemoryEntry `json:"entries"`
}

// ========== MemoryHandler HTTP 处理器 ==========

// MemoryHandler 长期记忆 HTTP 处理器
type MemoryHandler struct {
	memoryStore MemoryStore
}

// NewMemoryHandler 创建长期记忆 HTTP 处理器
func NewMemoryHandler(memoryStore MemoryStore) *MemoryHandler {
	return &MemoryHandler{
		memoryStore: memoryStore,
	}
}

// ListMemories 列出当前用户的长期记忆
// GET /api/memory/list?category=
func (h *MemoryHandler) ListMemories(c *gin.Context) {
	uc, ok := pkgmodel.UserContextFromCtx(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{
			"code": "UNAUTHORIZED", "message": "session invalid or expired",
		}})
		return
	}

	category := c.Query("category")
	entries, err := h.memoryStore.List(c.Request.Context(), uc.UserID, category)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "INTERNAL_ERROR", "message": "failed to list memories",
		}})
		return
	}

	if entries == nil {
		entries = []*MemoryEntry{}
	}

	c.JSON(http.StatusOK, MemoryListResponse{Entries: entries})
}

// PutMemory 写入一条长期记忆
// POST /api/memory/put
func (h *MemoryHandler) PutMemory(c *gin.Context) {
	uc, ok := pkgmodel.UserContextFromCtx(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{
			"code": "UNAUTHORIZED", "message": "session invalid or expired",
		}})
		return
	}

	var req MemoryPutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code": "BAD_REQUEST", "message": err.Error(),
		}})
		return
	}

	// 分类默认值
	category := req.Category
	if category == "" {
		category = "fact"
	}

	// userId 从 AuthContext 获取，不从请求体获取
	entry := &MemoryEntry{
		UserID:   uc.UserID,
		Key:      req.Key,
		Value:    req.Value,
		Category: category,
	}

	if err := h.memoryStore.Put(c.Request.Context(), uc.UserID, entry); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "INTERNAL_ERROR", "message": "failed to save memory",
		}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// DeleteMemory 删除一条长期记忆
// DELETE /api/memory/:key
func (h *MemoryHandler) DeleteMemory(c *gin.Context) {
	uc, ok := pkgmodel.UserContextFromCtx(c.Request.Context())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{
			"code": "UNAUTHORIZED", "message": "session invalid or expired",
		}})
		return
	}

	key := c.Param("key")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code": "BAD_REQUEST", "message": "key is required",
		}})
		return
	}

	if err := h.memoryStore.Delete(c.Request.Context(), uc.UserID, key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "INTERNAL_ERROR", "message": "failed to delete memory",
		}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// ========== 辅助函数 ==========

// BuildMemoryInjectionForUser 为指定用户构造长期记忆注入消息
// 供 AgentHandler 调用
func BuildMemoryInjectionForUser(store MemoryStore, userID int64) []*MemoryEntry {
	// 使用 context.Background()，降级容错
	entries, err := store.List(nil, userID, "")
	if err != nil || entries == nil {
		return nil
	}
	return entries
}

// SaveMemoryFromConversation 从对话中提取并保存长期记忆
// 供 AgentHandler 后置调用，降级容错
// extractor 不为 nil 时优先使用 LLM 提取，失败则回退到规则提取
func SaveMemoryFromConversation(store MemoryStore, userID int64, userMessage string, extractor MemoryExtractor) {
	if !ShouldSaveMemory(userMessage) {
		return
	}

	var entries []*MemoryEntry
	if extractor != nil {
		entries = extractor.Extract(context.Background(), userMessage)
	}
	// LLM 提取失败或无结果时，回退到规则提取
	if len(entries) == 0 {
		entries = ExtractMemoryFromConversation(userMessage)
	}
	for _, entry := range entries {
		entry.UserID = userID
		_ = store.Put(nil, userID, entry) // 降级：写入失败不影响对话
	}
}
