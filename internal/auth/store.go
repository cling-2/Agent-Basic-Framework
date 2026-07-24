package auth

import (
	"context"
	"sync"

	"kingsoft-agent/pkg/model"
)

// UserStore 用户存储抽象接口（基础档为内存实现）
type UserStore interface {
	// GetByUsername 根据用户名查询用户
	GetByUsername(ctx context.Context, username string) (*model.User, error)
	// GetByID 根据ID查询用户
	GetByID(ctx context.Context, id int64) (*model.User, error)
}

// MemoryUserStore 基于内存的用户存储
type MemoryUserStore struct {
	mu     sync.RWMutex
	users  map[int64]*model.User  // id -> user
	byName map[string]*model.User // username -> user
	roles  map[int64]*model.Role  // id -> role
}

// NewMemoryUserStore 创建内存用户存储实例
func NewMemoryUserStore() *MemoryUserStore {
	return &MemoryUserStore{
		users:  make(map[int64]*model.User),
		byName: make(map[string]*model.User),
		roles:  make(map[int64]*model.Role),
	}
}

// Seed 初始化预置角色和用户数据
// passwords 明文传入，内部自动做 bcrypt 哈希
func (s *MemoryUserStore) Seed(roles []*model.Role, users []struct {
	Username string
	Password string // 明文，内部哈希
	RoleID   int64
}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, r := range roles {
		s.roles[r.ID] = r
	}

	for i, u := range users {
		hash, err := hashPassword(u.Password)
		if err != nil {
			return err
		}
		id := int64(i + 1)
		now := timeNow()
		user := &model.User{
			ID:           id,
			Username:     u.Username,
			PasswordHash: hash,
			RoleID:       u.RoleID,
			RoleName:     s.roles[u.RoleID].Name,
			Status:       model.UserEnabled,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		s.users[id] = user
		s.byName[u.Username] = user
	}
	return nil
}

// GetByUsername 根据用户名查询用户
func (s *MemoryUserStore) GetByUsername(_ context.Context, username string) (*model.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.byName[username]
	if !ok {
		return nil, ErrUserNotFound
}

	// 填充 RoleName
	if role, ok := s.roles[u.RoleID]; ok {
		u.RoleName = role.Name
	}
	return u, nil
}

// GetByID 根据ID查询用户
func (s *MemoryUserStore) GetByID(_ context.Context, id int64) (*model.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	if role, ok := s.roles[u.RoleID]; ok {
		u.RoleName = role.Name
	}
	return u, nil
}

// GetRoleByID 根据ID查询角色
func (s *MemoryUserStore) GetRoleByID(_ context.Context, id int64) (*model.Role, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.roles[id]
	if !ok {
		return nil, ErrRoleNotFound
	}
	return r, nil
}
