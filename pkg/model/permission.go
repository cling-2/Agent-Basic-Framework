package model

// Permission 权限实体
type Permission struct {
	ID          int64  `json:"id"`
	ToolName    string `json:"tool_name"`
	Action      string `json:"action"`
	Description string `json:"description"`
}

// RolePermission 角色权限关联实体
type RolePermission struct {
	ID           int64 `json:"id"`
	RoleID       int64 `json:"role_id"`
	PermissionID int64 `json:"permission_id"`
}
