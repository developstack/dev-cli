// Package agents 描述各 AI 编码工具的**安装探测、项目级目录与启动方式**。
//
// 学习点（为什么"启动"也归这里管）：各工具的 auth 入口完全不同 ——
//   - Claude Code 认 `ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN` 环境变量（优先级高于 OAuth 登录）；
//   - pi 认 `models.json` 里的 provider（`baseUrl` + `api` + `apiKey`），而 `apiKey` 支持 `$ENV` 插值。
//
// 共同点是：**都能靠"启动时注入环境变量"把流量指向平台** —— 于是 dev-cli 不改任何工具配置文件，
// 密钥只活在子进程里（不落盘、不污染全局、不需要处理 gitignore 冲突）。
package agents

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Agent 是一个 AI 编码工具。
type Agent struct {
	// ID 标识（claude / pi）。
	ID string
	// Name 展示名。
	Name string
	// Binary 可执行文件名（用于探测与启动）。
	Binary string
	// NpmPackage 安装用的 npm 包名（缺依赖时提示用户装）。
	NpmPackage string
	// SkillsDir 项目级技能目录（相对项目根）。
	SkillsDir string
	// DefaultArgs 启动时的默认参数（用户参数追加在后面）。
	DefaultArgs []string
	// envKey 是"把网关密钥传给它"的环境变量名（空 = 该工具走别的机制）。
	envKey string
	// baseURLKey 是"把网关地址传给它"的环境变量名。
	baseURLKey string
}

// All 返回支持的工具清单（顺序稳定，便于输出）。
func All() []Agent {
	return []Agent{
		{
			ID: "claude", Name: "Claude Code", Binary: "claude",
			NpmPackage: "@anthropic-ai/claude-code",
			SkillsDir:  ".claude/skills",
			// 学习点：`bypassPermissions` 是 Claude Code 的权限模式之一
			// （choices: acceptEdits/auto/bypassPermissions/manual/dontAsk/plan）。
			DefaultArgs: []string{"--permission-mode", "bypassPermissions"},
			baseURLKey:  "ANTHROPIC_BASE_URL",
			envKey:      "ANTHROPIC_AUTH_TOKEN",
		},
		{
			ID: "pi", Name: "pi", Binary: "pi",
			NpmPackage: "@earendil-works/pi-coding-agent",
			SkillsDir:  ".pi/skills",
			// pi 的 provider 定义在 models.json，密钥用 `$DEV_CLI_GATEWAY_KEY` 引用 —— 见 EnvFor。
			baseURLKey: "DEV_CLI_GATEWAY_URL",
			envKey:     "DEV_CLI_GATEWAY_KEY",
		},
	}
}

// Lookup 按 id 找一个工具。
func Lookup(id string) (Agent, bool) {
	for _, a := range All() {
		if a.ID == id {
			return a, true
		}
	}
	return Agent{}, false
}

// IDs 返回全部工具 id（用于错误提示）。
func IDs() []string {
	out := make([]string, 0, len(All()))
	for _, a := range All() {
		out = append(out, a.ID)
	}
	return out
}

// Detect 返回可执行文件路径；未安装时 ok=false。
func (a Agent) Detect() (string, bool) {
	path, err := exec.LookPath(a.Binary)
	if err != nil {
		return "", false
	}
	return path, true
}

// GatewayBase 返回该工具要用的网关根地址。
//
// 学习点（容易踩的坑）：两种协议的"根"不一样 ——
//   - OpenAI 形状（pi）的 base 要含 `/v1`；
//   - Claude Code 的 `ANTHROPIC_BASE_URL` **只到域名**（它自己拼 `/v1/messages`），
//     传成 `.../v1` 会变成 `/v1/v1/messages`。
//
// 平台下发的是 OpenAI 形状，所以给 Claude Code 时要剥掉 `/v1`。
func (a Agent) GatewayBase(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if a.baseURLKey == "ANTHROPIC_BASE_URL" {
		// Claude Code 自己拼 /v1/messages —— 传成 .../v1 会变成 /v1/v1/messages。
		return strings.TrimSuffix(base, "/v1")
	}
	// OpenAI 形状的 base 要含 /v1。平台的配置项有时写成域名（没带 /v1），这里补齐，
	// 免得因为一个后缀差异让模型整个不可用。
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return base + "/v1"
}

// EnvFor 返回"把网关指向平台"要注入的环境变量。
//
// 学习点：两个工具语义相同、变量名不同 —— 统一在这里吃掉差异，上层只管"给我 env"。
func (a Agent) EnvFor(gatewayURL, apiKey string) []string {
	if a.envKey == "" {
		return nil
	}
	return []string{a.baseURLKey + "=" + gatewayURL, a.envKey + "=" + apiKey}
}

// SkillDirs 返回所有工具的项目级技能目录（相对项目根，去重且有序）。
func SkillDirs() []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(All()))
	for _, a := range All() {
		if _, dup := seen[a.SkillsDir]; dup {
			continue
		}
		seen[a.SkillsDir] = struct{}{}
		out = append(out, a.SkillsDir)
	}
	sort.Strings(out)
	return out
}

// EnsureDirs 创建所有工具的项目级技能目录（幂等）。
func EnsureDirs(projectRoot string) error {
	for _, dir := range SkillDirs() {
		if err := os.MkdirAll(filepath.Join(projectRoot, dir), 0o755); err != nil {
			return fmt.Errorf("dev-cli: 创建 %s 失败: %w", dir, err)
		}
	}
	return nil
}

// InstallCommand 返回安装该工具的命令（展示 + 执行都用它）。
//
// 学习点：把命令**原样返回给用户看**再执行 —— 装软件是改用户环境的事，
// 让用户知道到底跑的是什么（而不是"帮忙装一下"然后偷偷 npm i -g）。
func (a Agent) InstallCommand() []string {
	return []string{"npm", "install", "-g", a.NpmPackage}
}
