// Package claude 是 Claude Code（按 2.1.285 核实）的适配器（设计稿 §9.2）。
//
// 隔离：CLAUDE_CONFIG_DIR 指向托管目录，settings、会话、插件、技能、.claude.json 全部换位，macOS 钥匙串条目按配置
// 目录区分，新目录没有登录态。凭据只经环境变量给出：ANTHROPIC_AUTH_TOKEN（Bearer，优先级高于 API_KEY 与 /login）
// 与 MCP 头用的独立变量 AIDEV_MCP_TOKEN（AUTH_TOKEN 在远程 MCP 头里会被刻意展开为空）。
//
// 托管文件：settings.json（经 --settings 加载，优先级高于 user / project / local：路由、模型与噪音开关放在它的
// env 里，即使项目开启了"信任仓库配置"也盖不过它）与 mcp.json（--mcp-config；不信任仓库配置时加
// --strict-mcp-config，仓库内的 .mcp.json 被忽略）。不信任仓库配置时另加 --setting-sources user（Q4）。
// 不再传 --permission-mode bypassPermissions（旧 CLI 的做法，F10）。
package claude

import (
	"path/filepath"
	"regexp"

	"github.com/developstack/aidevstack/cli/internal/adapter"
	"github.com/developstack/aidevstack/cli/internal/api"
	"github.com/developstack/aidevstack/cli/internal/fsutil"
	"github.com/developstack/aidevstack/cli/internal/managed"
)

// Adapter 是 Claude Code 适配器。
type Adapter struct{}

// New 创建适配器。
func New() Adapter { return Adapter{} }

// Name 实现 adapter.Adapter。
func (Adapter) Name() string { return "claude" }

// Binary 实现 adapter.Adapter。
func (Adapter) Binary() string { return "claude" }

// MinVersion 实现 adapter.Adapter。
func (Adapter) MinVersion(m api.DevenvManifest) string { return m.Policy.Agents.Claude.MinVersion }

// UpgradeHint 实现 adapter.Adapter。
func (Adapter) UpgradeHint() string {
	return "claude update   (or: npm install -g @anthropic-ai/claude-code@latest)"
}

var versionPattern = regexp.MustCompile(`\d+\.\d+\.\d+`)

// ParseVersion 实现 adapter.Adapter（`claude --version` 输出 "2.1.285 (Claude Code)"）。
func (Adapter) ParseVersion(output string) (string, bool) {
	v := versionPattern.FindString(output)
	return v, v != ""
}

// Forbidden 实现 adapter.Adapter：托管目录里 /login 留下的凭据文件（Linux / Windows；macOS 在钥匙串里）每次删除。
func (Adapter) Forbidden() []string { return []string{".credentials.json"} }

// Files 实现 adapter.Adapter。
func (Adapter) Files(in adapter.RenderInput) ([]managed.File, error) {
	settings, err := fsutil.MarshalIndent(renderSettings(in))
	if err != nil {
		return nil, err
	}
	mcp, err := fsutil.MarshalIndent(renderMCP(in.Manifest))
	if err != nil {
		return nil, err
	}
	return []managed.File{
		{Path: "settings.json", Data: settings, Mode: 0o600},
		{Path: "mcp.json", Data: mcp, Mode: 0o600},
	}, nil
}

// blockedFlags 是与托管冲突、不允许透传的 claude 参数。
var blockedFlags = map[string]string{
	"--settings":          "dev-cli passes the managed settings file",
	"--setting-sources":   "dev-cli decides which setting sources load (project policy)",
	"--mcp-config":        "dev-cli passes the platform MCP configuration",
	"--strict-mcp-config": "dev-cli decides whether repository MCP servers load (project policy)",
}

// Launch 实现 adapter.Adapter：进程环境只放秘密与配置目录，路由与模型在 --settings 文件的 env 里。
func (Adapter) Launch(in adapter.LaunchInput) (adapter.Launch, error) {
	if err := adapter.RejectFlags(in.Passthrough, blockedFlags); err != nil {
		return adapter.Launch{}, err
	}
	args := []string{
		"--settings", filepath.Join(in.AgentDir, "settings.json"),
		"--mcp-config", filepath.Join(in.AgentDir, "mcp.json"),
	}
	if !in.Manifest.Policy.TrustRepoAgentConfig {
		args = append(args, "--setting-sources", "user", "--strict-mcp-config")
	}
	args = append(args, in.Passthrough...)
	return adapter.Launch{Args: args, Env: map[string]string{
		"CLAUDE_CONFIG_DIR":    in.AgentDir,
		"ANTHROPIC_AUTH_TOKEN": in.Secret.Reveal(),
		adapter.EnvMCPToken:    in.Secret.Reveal(),
	}}, nil
}
