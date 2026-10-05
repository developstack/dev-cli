// Package paths 决定 dev-cli 的用户级目录（设计稿 §4.5）。
//
//   - Config：平台地址等偏好（无秘密）；钥匙串不可用时的 CLI 凭据回退文件也在这里（0600）。
//     macOS ~/Library/Application Support/aidevstack，Linux $XDG_CONFIG_HOME|~/.config/aidevstack，
//     Windows %APPDATA%\aidevstack
//   - State：托管的 agent 配置目录（agents/<project_id>/{pi,claude}）、清单缓存、工作区索引。
//     Linux $XDG_STATE_HOME|~/.local/state/aidevstack；macOS 同 Config；Windows %LOCALAPPDATA%\aidevstack
//
// AIDEVSTACK_HOME 设置时三者都放在它下面（测试、CI 与多账号隔离用）。
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// appName 是目录名。
const appName = "aidevstack"

// Dirs 是用户级目录。
type Dirs struct {
	Config string
	State  string
}

// Resolve 解析目录（不创建）。
func Resolve() (Dirs, error) {
	if home := os.Getenv("AIDEVSTACK_HOME"); home != "" {
		return Dirs{Config: filepath.Join(home, "config"), State: filepath.Join(home, "state")}, nil
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return Dirs{}, fmt.Errorf("paths: user config dir: %w", err)
	}
	state, err := stateBase(config)
	if err != nil {
		return Dirs{}, err
	}
	return Dirs{Config: filepath.Join(config, appName), State: filepath.Join(state, appName)}, nil
}

// stateBase 返回状态目录的基址（各平台约定见包注释）。
func stateBase(config string) (string, error) {
	switch runtime.GOOS {
	case "windows":
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return local, nil
		}
		return "", errors.New("paths: %LOCALAPPDATA% is not set")
	case "darwin":
		return config, nil
	default:
		if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
			return xdg, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("paths: home dir: %w", err)
		}
		return filepath.Join(home, ".local", "state"), nil
	}
}

// AgentDir 是某项目某 agent 的托管配置目录（PI_CODING_AGENT_DIR / CLAUDE_CONFIG_DIR）。
func (d Dirs) AgentDir(projectID, agent string) string {
	return filepath.Join(d.State, "agents", projectID, agent)
}

// ManifestCache 是某项目清单的本地缓存（304 时复用）。
func (d Dirs) ManifestCache(projectID string) string {
	return filepath.Join(d.State, "agents", projectID, "manifest.json")
}

// SkillCache 是技能包缓存（按 digest 命名，多个项目 / agent 共用；内容不可变，可随时清空）。
func (d Dirs) SkillCache() string {
	return filepath.Join(d.State, "cache", "skills")
}

// WorkspaceIndex 记录本机绑定过的仓库根（logout --all 据此删除各仓库的开发者密钥凭证）。
func (d Dirs) WorkspaceIndex() string {
	return filepath.Join(d.State, "workspaces.json")
}

// UserConfigFile 是用户偏好文件。
func (d Dirs) UserConfigFile() string { return filepath.Join(d.Config, "config.json") }

// CredentialFallbackFile 是钥匙串不可用时的 CLI 凭据回退文件（0600）。
func (d Dirs) CredentialFallbackFile() string { return filepath.Join(d.Config, "credentials.json") }
