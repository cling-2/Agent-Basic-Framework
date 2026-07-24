package model

import "time"

// UserStatus 用户状态
type UserStatus int

const (
	UserEnabled  UserStatus = 1 // 启用
	UserDisabled UserStatus = 0 // 禁用
)

// User 用户实体
type User struct {
	ID           int64      `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"-"` // 永不暴露到 JSON
	RoleID       int64      `json:"role_id"`
	RoleName     string     `json:"role"` // 由查询时填充，非数据库字段
	Status       UserStatus `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// Role 角色实体
type Role struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// 预置角色名常量
const (
	RoleAdmin   = "admin"   // 管理员，可调用所有工具
	RoleVisitor = "visitor" // 访客，仅可调用基础工具
)
