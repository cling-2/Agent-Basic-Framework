package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// LLMSettings LLM 配置
type LLMSettings struct {
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
}

// Configured 返回是否已配置真实LLM
func (s *LLMSettings) Configured() bool {
	return s.APIKey != ""
}

// MaskedAPIKey 返回脱敏后的API Key
func (s *LLMSettings) MaskedAPIKey() string {
	if s.APIKey == "" {
		return ""
	}
	if len(s.APIKey) <= 8 {
		return "****"
	}
	return s.APIKey[:4] + strings.Repeat("*", len(s.APIKey)-8) + s.APIKey[len(s.APIKey)-4:]
}

// SettingsStore 配置持久化存储
type SettingsStore struct {
	mu       sync.RWMutex
	filePath string
	settings LLMSettings
}

// NewSettingsStore 创建配置存储
func NewSettingsStore(filePath string) *SettingsStore {
	return &SettingsStore{
		filePath: filePath,
	}
}

// Load 从文件加载配置
func (s *SettingsStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 文件不存在不是错误
		}
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	if len(data) == 0 {
		return nil
	}

	if err := json.Unmarshal(data, &s.settings); err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}

	return nil
}

// Get 读取当前配置（返回副本）
func (s *SettingsStore) Get() LLMSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// Save 保存配置到文件
func (s *SettingsStore) Save(settings LLMSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.settings = settings

	// 确保目录存在
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}

	data, err := json.MarshalIndent(s.settings, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	// 原子写入：先写临时文件，再重命名
	tmpFile := s.filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	if err := os.Rename(tmpFile, s.filePath); err != nil {
		// Windows上rename可能失败，回退到直接写入
		if err := os.WriteFile(s.filePath, data, 0644); err != nil {
			return fmt.Errorf("写入配置文件失败: %w", err)
		}
		os.Remove(tmpFile)
	}

	return nil
}

// IsConfigured 返回是否已配置LLM
func (s *SettingsStore) IsConfigured() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings.Configured()
}
