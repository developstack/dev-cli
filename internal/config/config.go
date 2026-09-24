// Package config 读写项目级的 `.dev-cli/settings.json`。
//
// 学习点（为什么放在**项目里**而不是用户主目录）：绑定关系是"这个项目属于平台上的哪个项目"，
// 天然是项目属性 —— 放项目里，换机器 clone 下来只要重新 init 一次即可；
// 放主目录则会变成"一台机器只能绑一个项目"。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DirName 是本工具在项目里的目录名。
const DirName = ".dev-cli"

// FileName 是设置文件名。
const FileName = "settings.json"

// ErrNotInitialized 表示项目还没初始化（`.dev-cli/settings.json` 不存在）。
//
// 学习点：**未认证是正常状态**，不是故障 —— 调用方据此提示"请先 dev-cli init"，
// 而不是抛一个看不懂的错误。
var ErrNotInitialized = errors.New("dev-cli: not initialized")

// Settings 是项目级设置（含 API Key，所以文件权限必须是 0600）。
type Settings struct {
	// APIKey 平台签发的密钥（存在项目里，所以**必须**被 .gitignore 挡住）。
	APIKey string `json:"api_key"`
	// Endpoint 平台根地址（不带 /api）。
	Endpoint string `json:"endpoint"`
	// ProjectID 绑定的平台项目 id。
	ProjectID string `json:"project_id"`
	// ProjectName 项目展示名（给提示信息用）。
	ProjectName string `json:"project_name,omitempty"`
	// AgentID 本机标识（上报安装状态时用；换机器会变）。
	AgentID string `json:"agent_id"`
	// CreatedAt 初始化时间。
	CreatedAt time.Time `json:"created_at"`
}

// Path 返回设置文件路径。
func Path(projectRoot string) string {
	return filepath.Join(projectRoot, DirName, FileName)
}

// Load 读取设置；不存在时返回 ErrNotInitialized。
func Load(projectRoot string) (Settings, error) {
	raw, err := os.ReadFile(Path(projectRoot))
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, ErrNotInitialized
	}
	if err != nil {
		return Settings{}, fmt.Errorf("dev-cli: 读取设置失败: %w", err)
	}
	var s Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		return Settings{}, fmt.Errorf("dev-cli: 解析设置失败: %w", err)
	}
	if s.APIKey == "" || s.ProjectID == "" {
		return Settings{}, ErrNotInitialized
	}
	return s, nil
}

// Save 写入设置（目录不存在会自动建，权限 0600 —— 里面有密钥）。
func Save(projectRoot string, s Settings) error {
	dir := filepath.Join(projectRoot, DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("dev-cli: 创建设置目录失败: %w", err)
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("dev-cli: 序列化设置失败: %w", err)
	}
	if err := os.WriteFile(Path(projectRoot), append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("dev-cli: 写入设置失败: %w", err)
	}
	return nil
}
