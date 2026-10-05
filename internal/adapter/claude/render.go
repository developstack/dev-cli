package claude

import (
	"slices"
	"strconv"

	"github.com/developstack/aidevstack/cli/internal/adapter"
	"github.com/developstack/aidevstack/cli/internal/api"
)

// settingsFile 是托管 settings.json（经 --settings 加载，优先级最高）。
type settingsFile struct {
	// Env 路由、模型与噪音开关（不含秘密；2.1.285 实测：--settings 的 env 盖过项目 settings 的 env）。
	Env map[string]string `json:"env"`
	// EnableAllProjectMcpServers 不自动启用仓库 .mcp.json 里的服务器（信任仓库配置时也要逐个确认）。
	EnableAllProjectMcpServers bool `json:"enableAllProjectMcpServers"`
}

// renderSettings 渲染 settings.json。
//
// 模型：ANTHROPIC_MODEL 与 sonnet / opus 位是项目默认模型，haiku 位（后台小任务）是 small_model；
// ANTHROPIC_SMALL_FAST_MODEL 已废弃，不写。默认模型的上游不是 Anthropic 时关掉实验性 beta 头，并把它的上下文窗口
// 写进 CLAUDE_CODE_MAX_CONTEXT_TOKENS：Claude Code（2.1.285 实测）不认识的模型名一律按 200k 管 auto-compact，
// 窗口更小的模型（多数 128k）会在压缩之前就超窗被上游拒绝。只按默认模型设：会话里 /model 换到窗口更小的模型时仍可能超窗。
func renderSettings(in adapter.RenderInput) settingsFile {
	m := in.Manifest
	client := "X-Aidevstack-Client: " + adapter.ClientHeader("claude-code", in.CLIVersion)
	env := map[string]string{
		"ANTHROPIC_BASE_URL":                       m.Gateway.AnthropicBaseURL,
		"ANTHROPIC_MODEL":                          m.Defaults.Model,
		"ANTHROPIC_DEFAULT_SONNET_MODEL":           m.Defaults.Model,
		"ANTHROPIC_DEFAULT_OPUS_MODEL":             m.Defaults.Model,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":            m.Defaults.SmallModel,
		"ANTHROPIC_CUSTOM_HEADERS":                 client,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
	}
	if model, ok := defaultModel(m); ok && !slices.Contains(model.Protocols, api.AnthropicMessages) {
		env["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"] = "1"
		if model.ContextWindow > 0 {
			env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] = strconv.Itoa(model.ContextWindow)
		}
	}
	addRouteOption(env, m)
	return settingsFile{Env: env, EnableAllProjectMcpServers: false}
}

// addRouteOption 把项目路由别名放进 /model 选择器（ADR-0027：路由名就是客户端用的 model）。
//
// Claude Code（2.1.285 二进制里的 ANTHROPIC_CUSTOM_MODEL_OPTION / _NAME / _DESCRIPTION）只有**一个**自定义选项位：
// 默认模型本身是路由时它已在默认位上，这里放清单里第一个不是默认模型的路由；没有路由不写。
// 其余路由仍可用 `/model <名字>` 或 `--model` 直接选（网关按名字解析，不依赖选择器）。
func addRouteOption(env map[string]string, m api.DevenvManifest) {
	for i := range m.Models {
		route := m.Models[i]
		if route.Kind != api.Route || route.ID == m.Defaults.Model {
			continue
		}
		env["ANTHROPIC_CUSTOM_MODEL_OPTION"] = route.ID
		env["ANTHROPIC_CUSTOM_MODEL_OPTION_NAME"] = route.DisplayName
		env["ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION"] = "项目路由：平台按候选顺序自动重试与切换"
		return
	}
}

// defaultModel 取清单里的默认模型（不在清单里时 ok=false：不猜它的协议与窗口）。
func defaultModel(m api.DevenvManifest) (api.DevenvModel, bool) {
	for i := range m.Models {
		if m.Models[i].ID == m.Defaults.Model {
			return m.Models[i], true
		}
	}
	return api.DevenvModel{}, false
}

// mcpFile 是托管 mcp.json（--mcp-config；MCP 未开启时为空，配合 --strict-mcp-config 屏蔽仓库的 .mcp.json）。
type mcpFile struct {
	MCPServers map[string]mcpServer `json:"mcpServers"`
}

type mcpServer struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

func renderMCP(m api.DevenvManifest) mcpFile {
	out := mcpFile{MCPServers: map[string]mcpServer{}}
	if m.Mcp.Enabled && m.Gateway.McpURL != "" {
		out.MCPServers[adapter.ProviderName] = mcpServer{
			Type: "http", URL: m.Gateway.McpURL,
			Headers: map[string]string{"Authorization": "Bearer ${" + adapter.EnvMCPToken + "}"},
		}
	}
	return out
}
