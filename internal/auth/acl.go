package auth

import (
	"context"
	"sync"

	"kingsoft-agent/pkg/model"
)

// ACLChecker 访问控制检查器接口
type ACLChecker interface {
	// Allowed 判断指定角色是否允许对某工具执行某动作
	Allowed(ctx context.Context, role string, toolName string, action string) (bool, error)

	// PermissionsForRole 获取指定角色的所有权限列表
	PermissionsForRole(ctx context.Context, role string) ([]model.Permission, error)
}

// MemoryACLChecker 基于内存 map 的 ACL 检查器
// rolePermissions: map[role] -> map[toolName] -> map[action] -> bool
type MemoryACLChecker struct {
	mu              sync.RWMutex
	rolePermissions map[string]map[string]map[string]bool
	permissions     map[string]model.Permission // "toolName:action" -> Permission
	superRoles      map[string]bool             // 超级角色白名单（默认拥有所有权限）
}

// NewMemoryACLChecker 创建内存 ACL 检查器
func NewMemoryACLChecker() *MemoryACLChecker {
	return &MemoryACLChecker{
		rolePermissions: make(map[string]map[string]map[string]bool),
		permissions:     make(map[string]model.Permission),
		superRoles:      make(map[string]bool),
	}
}

// AddSuperRole 添加超级角色（拥有所有权限）
func (c *MemoryACLChecker) AddSuperRole(role string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.superRoles[role] = true
}

// Grant 授予角色对某工具某动作的权限
func (c *MemoryACLChecker) Grant(role string, perm model.Permission) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := perm.ToolName + ":" + perm.Action
	c.permissions[key] = perm

	if _, ok := c.rolePermissions[role]; !ok {
		c.rolePermissions[role] = make(map[string]map[string]bool)
	}
	if _, ok := c.rolePermissions[role][perm.ToolName]; !ok {
		c.rolePermissions[role][perm.ToolName] = make(map[string]bool)
	}
	c.rolePermissions[role][perm.ToolName][perm.Action] = true
}

// Allowed 判断指定角色是否允许对某工具执行某动作
func (c *MemoryACLChecker) Allowed(_ context.Context, role string, toolName string, action string) (bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 超级角色默认拥有所有权限
	if c.superRoles[role] {
		return true, nil
	}

	tools, ok := c.rolePermissions[role]
	if !ok {
		return false, nil
	}
	actions, ok := tools[toolName]
	if !ok {
		return false, nil
	}
	allowed, ok := actions[action]
	if !ok {
		return false, nil
	}
	return allowed, nil
}

// PermissionsForRole 获取指定角色的所有权限列表
func (c *MemoryACLChecker) PermissionsForRole(_ context.Context, role string) ([]model.Permission, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 超级角色返回空列表（调用方应理解为拥有所有权限）
	if c.superRoles[role] {
		return nil, nil
	}

	tools, ok := c.rolePermissions[role]
	if !ok {
		return nil, nil
	}

	var perms []model.Permission
	for toolName, actions := range tools {
		for action, allowed := range actions {
			if allowed {
				key := toolName + ":" + action
				if p, ok := c.permissions[key]; ok {
					perms = append(perms, p)
				}
			}
		}
	}
	return perms, nil
}
