// Package pi 是 pi（@earendil-works/pi-coding-agent ≥ 0.99，按 1.0.0 核实）的适配器（设计稿 §9.1）。
//
// 隔离：PI_CODING_AGENT_DIR 指向托管目录，用户自己的 ~/.pi/agent（auth.json、settings、models、mcp、extensions）
// 一律不读。托管文件：
//   - models.json：provider "aidevstack"（apiKey 与头经 ${AIDEV_TOKEN} 插值，文件里不含秘密），按模型选上游协议；
//   - settings.json：默认 provider / 模型 / 思考强度、enabledModels 只含平台模型、关闭安装遥测、项目信任默认值；
//   - mcp.json：清单开启 MCP 时写入平台聚合端点 /v1/mcp（P9 在 pi 1.0.0 上实测：pi mcp list 与模型调用工具均成功）。
//
// 启动参数：`-ns`（关掉 ~/.agents/skills、托管目录 skills/ 与项目技能的自动发现）+ 每个下发技能一个 `--skill`
// （P8 在 pi 1.0.0 上实测：-ns 之后显式 --skill 仍然加载）+ `-na` / `-a`（是否信任仓库内 .pi/ 配置，Q4）。
package pi

import (
	"path/filepath"
	"regexp"

	"github.com/developstack/aidevstack/cli/internal/adapter"
	"github.com/developstack/aidevstack/cli/internal/api"
	"github.com/developstack/aidevstack/cli/internal/fsutil"
	"github.com/developstack/aidevstack/cli/internal/managed"
)

// Adapter 是 pi 适配器。
type Adapter struct{}

// New 创建适配器。
func New() Adapter { return Adapter{} }

// Name 实现 adapter.Adapter。
func (Adapter) Name() string { return "pi" }

// Binary 实现 adapter.Adapter。
func (Adapter) Binary() string { return "pi" }

// MinVersion 实现 adapter.Adapter。
func (Adapter) MinVersion(m api.DevenvManifest) string { return m.Policy.Agents.Pi.MinVersion }

// UpgradeHint 实现 adapter.Adapter（`@mariozechner/pi-coding-agent` 已废弃，迁到新包名）。
func (Adapter) UpgradeHint() string { return "npm install -g @earendil-works/pi-coding-agent@latest" }

var versionPattern = regexp.MustCompile(`\d+\.\d+\.\d+[0-9A-Za-z.+-]*`)

// ParseVersion 实现 adapter.Adapter（`pi --version` 输出 "1.0.0"）。
func (Adapter) ParseVersion(output string) (string, bool) {
	v := versionPattern.FindString(output)
	return v, v != ""
}

// Forbidden 实现 adapter.Adapter：托管目录里 /login 产生的 auth.json 每次删除（平台托管模式不支持自带凭据）。
func (Adapter) Forbidden() []string { return []string{"auth.json"} }

// Files 实现 adapter.Adapter。
func (Adapter) Files(in adapter.RenderInput) ([]managed.File, error) {
	models, err := fsutil.MarshalIndent(renderModels(in))
	if err != nil {
		return nil, err
	}
	settings, err := fsutil.MarshalIndent(renderSettings(in.Manifest))
	if err != nil {
		return nil, err
	}
	files := []managed.File{
		{Path: "models.json", Data: models, Mode: 0o600},
		{Path: "settings.json", Data: settings, Mode: 0o600},
	}
	if in.Manifest.Mcp.Enabled && in.Manifest.Gateway.McpURL != "" {
		mcp, err := fsutil.MarshalIndent(renderMCP(in.Manifest))
		if err != nil {
			return nil, err
		}
		files = append(files, managed.File{Path: "mcp.json", Data: mcp, Mode: 0o600})
	}
	return files, nil
}

// blockedFlags 是与托管冲突、不允许透传的 pi 参数。
var blockedFlags = map[string]string{
	"--api-key":  "the platform key is provided by dev-cli",
	"--provider": "dev-cli manages the provider (use --model to pick a platform model)",
}

// untrustedFlags 在项目不信任仓库内 agent 配置时拒绝透传（与托管的 -na 冲突，Q4）。
var untrustedFlags = map[string]string{
	"-a":        "this project does not trust repository agent config",
	"--approve": "this project does not trust repository agent config",
}

// Launch 实现 adapter.Adapter。
func (Adapter) Launch(in adapter.LaunchInput) (adapter.Launch, error) {
	if err := adapter.RejectFlags(in.Passthrough, blockedFlags); err != nil {
		return adapter.Launch{}, err
	}
	trust := in.Manifest.Policy.TrustRepoAgentConfig
	if !trust {
		if err := adapter.RejectFlags(in.Passthrough, untrustedFlags); err != nil {
			return adapter.Launch{}, err
		}
	}
	args := []string{"-ns"}
	for _, skill := range in.Manifest.Skills {
		args = append(args, "--skill", filepath.Join(in.AgentDir, "skills", skill.Name))
	}
	if trust {
		args = append(args, "-a")
	} else {
		args = append(args, "-na")
	}
	args = append(args, in.Passthrough...)
	return adapter.Launch{Args: args, Env: map[string]string{
		"PI_CODING_AGENT_DIR": in.AgentDir,
		adapter.EnvToken:      in.Secret.Reveal(),
		adapter.EnvMCPToken:   in.Secret.Reveal(),
		// 关闭安装 / 更新遥测与 provider 归属头（settings.enableInstallTelemetry 之外的进程级开关）。
		"PI_TELEMETRY": "0",
	}}, nil
}
