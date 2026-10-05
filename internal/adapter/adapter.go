// Package adapter 把与 agent 无关的清单渲染成具体 agent 的托管配置与启动参数（ADR-0025 决策 4 / 5，设计稿 §9）。
//
// 每个适配器只做三件事：渲染托管目录里的文件（不含秘密，密钥经环境变量引用）、列出每次都要删除的文件
// （自带凭据）、给出启动参数与环境。凭据、清单、目录、锁、exec 都由调用方（cli 的 start / sync）负责。
package adapter

import (
	"fmt"
	"slices"
	"strings"

	goversion "github.com/hashicorp/go-version"

	"github.com/developstack/aidevstack/cli/internal/api"
	"github.com/developstack/aidevstack/cli/internal/managed"
	"github.com/developstack/aidevstack/cli/internal/secret"
)

// ProviderName 是平台在 agent 里的 provider / MCP 服务器名。
const ProviderName = "aidevstack"

// 环境变量名（托管文件只引用名字，值由启动器设置；MCP 头必须用独立变量，见设计稿 §9.2）。
//
//nolint:gosec // G101 误报：这是环境变量名，不是凭据。
const (
	EnvToken    = "AIDEV_TOKEN"
	EnvMCPToken = "AIDEV_MCP_TOKEN"
)

// RenderInput 是渲染托管文件的输入。
type RenderInput struct {
	Manifest api.DevenvManifest
	// AgentDir 托管目录的绝对路径（技能路径等需要它）。
	AgentDir string
	// CLIVersion 写进 X-Aidevstack-Client 头（统计 client 维度）。
	CLIVersion string
}

// LaunchInput 是组装启动参数的输入。
type LaunchInput struct {
	Manifest api.DevenvManifest
	AgentDir string
	// Secret 开发者密钥明文（只进 agent 的环境变量）。
	Secret secret.Secret
	// Passthrough 用户在 `--` 之后给 agent 的参数。
	Passthrough []string
}

// Launch 是启动 agent 的参数（argv 不含程序名）与要设置的环境变量。
type Launch struct {
	Args []string
	Env  map[string]string
}

// Adapter 是一个 agent 的适配器。
type Adapter interface {
	// Name 是 `dev-cli start <name>` 里的名字，也是托管目录名。
	Name() string
	// Binary 是可执行文件名（在 PATH 里找）。
	Binary() string
	// MinVersion 是清单要求的最低版本（policy.agents.<name>.min_version）。
	MinVersion(m api.DevenvManifest) string
	// UpgradeHint 是版本过低时的升级命令。
	UpgradeHint() string
	// ParseVersion 从 `<binary> --version` 的输出里取版本号。
	ParseVersion(output string) (string, bool)
	// Files 渲染托管目录里的文件。
	Files(in RenderInput) ([]managed.File, error)
	// Forbidden 是托管目录里每次同步都要删除的文件（agent 自己的登录态 / 自带凭据）。
	Forbidden() []string
	// Launch 组装启动参数与环境；透传参数与托管冲突时返回错误。
	Launch(in LaunchInput) (Launch, error)
}

// VersionBelow 报告 installed 是否低于 minimum（任一方解析不了时不拦）。
func VersionBelow(installed, minimum string) bool {
	cur, err := goversion.NewVersion(installed)
	if err != nil || strings.TrimSpace(minimum) == "" {
		return false
	}
	floor, err := goversion.NewVersion(minimum)
	return err == nil && cur.LessThan(floor)
}

// ClientHeader 是 X-Aidevstack-Client 的值（"pi/dev-cli-2.0.0"）；平台只作统计，不作授权依据。
func ClientHeader(agent, cliVersion string) string { return agent + "/dev-cli-" + cliVersion }

// Protocol 为模型选上游协议：Anthropic 原生 → anthropic-messages（思考与缓存保真度最高），
// 其次 openai-responses，其余 openai-completions（设计稿 §6.1 的择优规则，事实来自清单）。
func Protocol(m api.DevenvModel) api.DevenvModelProtocols {
	for _, want := range []api.DevenvModelProtocols{
		api.AnthropicMessages, api.OpenaiResponses,
	} {
		if slices.Contains(m.Protocols, want) {
			return want
		}
	}
	return api.OpenaiCompletions
}

// RejectFlags 拒绝与托管冲突的透传参数（`--flag` 与 `--flag=value` 两种写法）。
func RejectFlags(args []string, blocked map[string]string) error {
	for _, arg := range args {
		if arg == "--" {
			return nil
		}
		name, _, _ := strings.Cut(arg, "=")
		if reason, ok := blocked[name]; ok {
			return fmt.Errorf("%s cannot be passed through dev-cli: %s", name, reason)
		}
	}
	return nil
}
