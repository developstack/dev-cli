// Package gateway 管理"平台下发的模型网关配置"（本项目专用）。
//
// 学习点（为什么单独存一份）：平台通过 `apply_model_config` 下发的是**虚拟密钥** ——
// 它只对平台 `/v1` 有效、可限预算、可吊销。把它存在 `.dev-cli/auth.json`（0600，且被 gitignore），
// `dev-cli start` 启动 agent 时再注入到子进程环境里，**工具配置文件里不会出现明文密钥**。
package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/developstack/dev-cli/internal/config"
)

// FileName 是网关配置文件名（放在 `.dev-cli/` 下）。
const FileName = "auth.json"

// ErrNotConfigured 表示平台还没下发模型配置（正常状态：管理员没配网关地址）。
var ErrNotConfigured = errors.New("dev-cli: 平台尚未下发模型网关配置")

// Config 是下发给本项目的网关配置。
type Config struct {
	// BaseURL 平台推理入口（形如 http://host:8080/v1）。
	BaseURL string `json:"base_url"`
	// APIKey 虚拟密钥明文（只对平台 /v1 有效）。
	APIKey string `json:"api_key"`
	// Provider 默认供应商（可选，仅展示）。
	Provider string `json:"provider,omitempty"`
	// Models 该项目允许的模型清单（空 = 不限）。
	Models []string `json:"models,omitempty"`
}

// Path 返回配置文件路径。
func Path(projectRoot string) string {
	return filepath.Join(projectRoot, config.DirName, FileName)
}

// Load 读取配置；不存在或字段不全时返回 ErrNotConfigured。
func Load(projectRoot string) (Config, error) {
	raw, err := os.ReadFile(Path(projectRoot))
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, ErrNotConfigured
	}
	if err != nil {
		return Config{}, fmt.Errorf("dev-cli: 读取网关配置失败: %w", err)
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("dev-cli: 解析网关配置失败: %w", err)
	}
	if c.BaseURL == "" || c.APIKey == "" {
		return Config{}, ErrNotConfigured
	}
	return c, nil
}

// Save 写入配置（0600：里面有虚拟密钥）。
func Save(projectRoot string, c Config) error {
	if err := os.MkdirAll(filepath.Join(projectRoot, config.DirName), 0o755); err != nil {
		return fmt.Errorf("dev-cli: 创建目录失败: %w", err)
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("dev-cli: 序列化网关配置失败: %w", err)
	}
	if err := os.WriteFile(Path(projectRoot), append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("dev-cli: 写入网关配置失败: %w", err)
	}
	return nil
}

// Remove 删除配置（`dev-cli auth disable` 用）。
func Remove(projectRoot string) error {
	if err := os.Remove(Path(projectRoot)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("dev-cli: 删除网关配置失败: %w", err)
	}
	return nil
}

// Masked 返回给用户看的密钥摘要（永不打全）。
func (c Config) Masked() string {
	if len(c.APIKey) <= 8 {
		return "****"
	}
	return c.APIKey[:4] + "…" + c.APIKey[len(c.APIKey)-4:]
}
