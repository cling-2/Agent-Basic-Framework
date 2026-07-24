package auth

import (
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// 错误定义
var (
	ErrUserNotFound    = errors.New("user not found")
	ErrRoleNotFound    = errors.New("role not found")
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionExpired  = errors.New("session expired")
)

// hashPassword 使用 bcrypt 哈希密码（cost=12）
func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// checkPassword 校验密码与 bcrypt 哈希是否匹配
func checkPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// timeNow 返回当前时间（可测试性：可在测试中替换）
var timeNow = time.Now
