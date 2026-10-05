// Package launcher 组装 agent 的环境并启动它（设计稿 §10.2 第 9 / 10 步）。
package launcher

import (
	"slices"
	"sort"
	"strings"
)

// 环境黑名单（设计稿 §9.1 / §9.2）：启动 agent 前从继承的环境里去掉"自带的厂商凭据与改道开关"，
// 这样 agent 的内置 provider 都没有凭据（pi 的 /model 里不会出现），Claude Code 也不会走自己的登录或云厂商。
// 只删名字匹配的变量，值从不读取、从不打印。
var (
	// blockedPrefixes 整个前缀都属于某个模型厂商 / 云（含 ANTHROPIC_BASE_URL 这类改道开关）。
	blockedPrefixes = []string{"ANTHROPIC_", "OPENAI_", "AZURE_", "AWS_", "CLAUDE_CODE_USE_"}
	// blockedSuffixes 各家 provider 的 API key 约定（DEEPSEEK_API_KEY、GEMINI_API_KEY、OPENROUTER_API_KEY …）。
	blockedSuffixes = []string{"_API_KEY"}
	// blockedExact 不符合上面约定的凭据与 dev-cli 自己托管的变量（后者由启动器重新设置）。
	blockedExact = []string{
		"CLAUDE_CODE_OAUTH_TOKEN", "GOOGLE_APPLICATION_CREDENTIALS", "HF_TOKEN", "COPILOT_GITHUB_TOKEN",
		"CLOUDFLARE_ACCOUNT_ID", "PI_CODING_AGENT_DIR", "CLAUDE_CONFIG_DIR", "AIDEV_TOKEN", "AIDEV_MCP_TOKEN",
	}
)

// Blocked 报告环境变量名是否在黑名单里（Windows 的环境变量名不区分大小写，统一按大写比较）。
func Blocked(name string) bool {
	upper := strings.ToUpper(name)
	if slices.Contains(blockedExact, upper) {
		return true
	}
	for _, prefix := range blockedPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	for _, suffix := range blockedSuffixes {
		if strings.HasSuffix(upper, suffix) {
			return true
		}
	}
	return false
}

// BuildEnv 从 base（KEY=VALUE 列表）去掉黑名单变量，再叠加 extra；返回新环境与被去掉的变量名（排序、去重）。
func BuildEnv(base []string, extra map[string]string) ([]string, []string) {
	out := make([]string, 0, len(base)+len(extra))
	var removed []string
	for _, kv := range base {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		if Blocked(name) {
			removed = append(removed, name)
			continue
		}
		if _, overridden := extra[name]; overridden {
			continue
		}
		out = append(out, kv)
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+extra[k])
	}
	sort.Strings(removed)
	return out, slices.Compact(removed)
}
