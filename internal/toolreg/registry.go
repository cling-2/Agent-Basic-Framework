package toolreg

import (
	"context"
	"sync"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// ToolRegistry 工具注册中心
// 管理工具的注册、查询和列表，为 Agent 提供 ToolsNodeConfig
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]einotool.BaseTool
}

// NewToolRegistry 创建工具注册中心
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]einotool.BaseTool),
	}
}

// Register 注册工具
func (r *ToolRegistry) Register(t einotool.BaseTool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	info, err := t.Info(context.Background())
	if err != nil {
		return err
	}
	r.tools[info.Name] = t
	return nil
}

// Get 根据名称获取工具
func (r *ToolRegistry) Get(name string) (einotool.BaseTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List 列出所有已注册工具
func (r *ToolRegistry) List() []einotool.BaseTool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]einotool.BaseTool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// Filter 根据名称列表获取工具子集
func (r *ToolRegistry) Filter(names []string) []einotool.BaseTool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]einotool.BaseTool, 0, len(names))
	for _, name := range names {
		if t, ok := r.tools[name]; ok {
			result = append(result, t)
		}
	}
	return result
}

// ToolInfos 获取所有工具的元信息
func (r *ToolRegistry) ToolInfos(ctx context.Context) ([]*schema.ToolInfo, error) {
	r.mu.RLock()
	tools := make([]einotool.BaseTool, 0, len(r.tools))
	for _, t := range r.tools {
		tools = append(tools, t)
	}
	r.mu.RUnlock()

	infos := make([]*schema.ToolInfo, 0, len(tools))
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// Names 返回所有已注册工具的名称列表
func (r *ToolRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}
