package pi

import (
	"github.com/developstack/aidevstack/cli/internal/adapter"
	"github.com/developstack/aidevstack/cli/internal/api"
)

// models.json 的形状（pi 1.0.0 dist/core/model-config.js 的 ModelsConfigSchema）。
type modelsFile struct {
	Providers map[string]provider `json:"providers"`
}

type provider struct {
	BaseURL string            `json:"baseUrl"`
	API     string            `json:"api"`
	APIKey  string            `json:"apiKey"`
	Headers map[string]string `json:"headers,omitempty"`
	Models  []model           `json:"models"`
}

// model 是一个模型条目；api / baseUrl 只在与 provider 默认不同时写（按模型覆盖协议）。
type model struct {
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	API           string   `json:"api,omitempty"`
	BaseURL       string   `json:"baseUrl,omitempty"`
	Reasoning     bool     `json:"reasoning,omitempty"`
	Input         []string `json:"input,omitempty"`
	ContextWindow int      `json:"contextWindow,omitempty"`
	MaxTokens     int      `json:"maxTokens,omitempty"`
	Cost          *cost    `json:"cost,omitempty"`
}

// cost 是每百万 token 的单价（pi 要求四项齐全）。
type cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// renderModels 渲染 models.json：provider 默认走 openai-completions（含 /v1 的 base），Anthropic 原生的模型
// 改用 anthropic-messages + 不含 /v1 的 base，OpenAI 原生的用 openai-responses。
func renderModels(in adapter.RenderInput) modelsFile {
	m := in.Manifest
	p := provider{
		BaseURL: m.Gateway.OpenaiBaseURL, API: string(api.OpenaiCompletions), APIKey: "${" + adapter.EnvToken + "}",
		Headers: map[string]string{"X-Aidevstack-Client": adapter.ClientHeader("pi", in.CLIVersion)},
		Models:  make([]model, 0, len(m.Models)),
	}
	for i := range m.Models {
		src := m.Models[i]
		item := model{
			ID: src.ID, Name: src.DisplayName, Reasoning: src.Reasoning, ContextWindow: src.ContextWindow,
			MaxTokens: src.MaxOutputTokens,
		}
		switch adapter.Protocol(src) {
		case api.AnthropicMessages:
			item.API, item.BaseURL = string(api.AnthropicMessages), m.Gateway.AnthropicBaseURL
		case api.OpenaiResponses:
			item.API = string(api.OpenaiResponses)
		case api.OpenaiCompletions:
		}
		for _, input := range src.Input {
			item.Input = append(item.Input, string(input))
		}
		if src.Cost != nil {
			item.Cost = &cost{
				Input: src.Cost.Input, Output: src.Cost.Output, CacheRead: src.Cost.CacheRead,
				CacheWrite: src.Cost.CacheWrite,
			}
		}
		p.Models = append(p.Models, item)
	}
	return modelsFile{Providers: map[string]provider{adapter.ProviderName: p}}
}

// settingsFile 是托管 settings.json（pi docs/settings.md）。
type settingsFile struct {
	DefaultProvider        string   `json:"defaultProvider"`
	DefaultModel           string   `json:"defaultModel,omitempty"`
	DefaultThinkingLevel   string   `json:"defaultThinkingLevel,omitempty"`
	EnabledModels          []string `json:"enabledModels"`
	EnableInstallTelemetry bool     `json:"enableInstallTelemetry"`
	// DefaultProjectTrust 与启动参数 -na / -a 一致（参数优先；这里保证 print / rpc 模式也是同一结论）。
	DefaultProjectTrust string   `json:"defaultProjectTrust"`
	Packages            []string `json:"packages"`
	Extensions          []string `json:"extensions"`
	Skills              []string `json:"skills"`
}

func renderSettings(m api.DevenvManifest) settingsFile {
	trust := "never"
	if m.Policy.TrustRepoAgentConfig {
		trust = "always"
	}
	// enabledModels 用 `**`：pi 以 minimatch 匹配 "<provider>/<模型 id>"，而平台模型 id 自带 `/`
	// （`opencode-go/deepseek-v4.1-flash`），`*` 不跨 `/`，写成 `aidevstack/*` 一个模型都匹配不上
	// （pi 1.0.0 启动即警告 `No models match pattern "aidevstack/*"`，模型轮换范围为空）。
	return settingsFile{
		DefaultProvider: adapter.ProviderName, DefaultModel: m.Defaults.Model,
		DefaultThinkingLevel: m.Defaults.ThinkingLevel, EnabledModels: []string{adapter.ProviderName + "/**"},
		DefaultProjectTrust: trust, Packages: []string{}, Extensions: []string{}, Skills: []string{},
	}
}

// mcpFile 是托管 mcp.json（pi docs/mcp.md：streamable HTTP，头支持 ${ENV}，拒绝 SSE）。
type mcpFile struct {
	MCPServers map[string]mcpServer `json:"mcpServers"`
}

type mcpServer struct {
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
	Exposure string            `json:"exposure,omitempty"`
}

func renderMCP(m api.DevenvManifest) mcpFile {
	return mcpFile{MCPServers: map[string]mcpServer{adapter.ProviderName: {
		URL:      m.Gateway.McpURL,
		Headers:  map[string]string{"Authorization": "Bearer ${" + adapter.EnvMCPToken + "}"},
		Exposure: string(m.Mcp.ExposureHint),
	}}}
}
