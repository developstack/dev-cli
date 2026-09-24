// Package piagent 维护 pi 的**托管 provider 条目**。
//
// 学习点（为什么必须动用户的全局配置）：pi 的 provider（baseUrl/api/models）只能写在 agent 目录的
// `models.json`，**项目级 `.pi/` 放不了**。所以要让 pi 走平台网关，只有两条路：
//  1. 把 provider 写进用户全局 models.json（我们选这条）；
//  2. 重定向 `PI_CODING_AGENT_DIR` 到项目里 —— 但那会**连带丢掉用户自己的技能/扩展/主题**，代价太大。
//
// 关键技巧：写进去的 provider **不含密钥** —— `apiKey` 用 `$DEV_CLI_GATEWAY_KEY` 引用，
// 真正的虚拟密钥由 `dev-cli start pi` 在启动时注入进程环境。这样"全局配置里没有秘密"。
package piagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ProviderName 是托管 provider 的名字（models.json 里的键）。
//
// 学习点：用固定名字而不是随机/项目名 —— 一台机器上指向同一个平台的入口只有一个，
// 多个项目共用它，凭据在启动时按项目注入。
const ProviderName = "dev-cli"

// EnvAPIKey 是 provider 里引用的环境变量名（由 dev-cli start 注入）。
const EnvAPIKey = "DEV_CLI_GATEWAY_KEY"

// ModelEntry 是写进 provider 的一个模型。
type ModelEntry struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Provider 是要写入的 provider 定义。
type Provider struct {
	BaseURL string
	API     string
	Models  []ModelEntry
}

// modelsFile 是 models.json 的最小形状（只保留我们要动与要保全的部分）。
type modelsFile struct {
	Providers map[string]map[string]any `json:"providers"`
	// 其余顶层键原样保留（避免把用户的其它配置吃掉）。
	rest map[string]any
}

// AgentDir 返回 pi 的 agent 目录（尊重 PI_CODING_AGENT_DIR）。
func AgentDir() string {
	if custom := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); custom != "" {
		return custom
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
}

// ModelsPath 返回 models.json 的路径。
func ModelsPath() string { return filepath.Join(AgentDir(), "models.json") }

// EnsureProvider 把托管 provider 合并进 models.json（幂等，不动其它 provider）。
//
// 学习点：**只改我们自己的那个键** —— 用户的 `deepseek` / `cc-switch-*` 等 provider 原样保留。
// 直接整体覆盖会把别人手工配的东西全弄没。
func EnsureProvider(p Provider) (string, error) {
	path := ModelsPath()
	if path == "" {
		return "", errors.New("dev-cli: 取不到 pi 的 agent 目录")
	}
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("dev-cli: 读取 %s 失败: %w", path, err)
	}

	var doc map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return "", fmt.Errorf("dev-cli: 解析 %s 失败（先备份再改）: %w", path, err)
		}
	}
	if doc == nil {
		doc = map[string]any{}
	}

	providers, _ := doc["providers"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	entry := map[string]any{
		"baseUrl": strings.TrimRight(p.BaseURL, "/"),
		"api":     orDefault(p.API, "openai-completions"),
		// 只引用环境变量：**密钥不落盘**。
		"apiKey": "$" + EnvAPIKey,
	}
	if models := toModelList(p.Models); len(models) > 0 {
		entry["models"] = models
	}
	providers[ProviderName] = entry
	doc["providers"] = providers

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("dev-cli: 序列化 %s 失败: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("dev-cli: 创建目录失败: %w", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("dev-cli: 写入 %s 失败: %w", path, err)
	}
	return path, nil
}

// RemoveProvider 摘掉托管 provider（`dev-cli auth disable` 用）。
func RemoveProvider() (bool, string, error) {
	path := ModelsPath()
	if path == "" {
		return false, "", nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, path, nil
	}
	if err != nil {
		return false, path, fmt.Errorf("dev-cli: 读取 %s 失败: %w", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false, path, fmt.Errorf("dev-cli: 解析 %s 失败: %w", path, err)
	}
	providers, _ := doc["providers"].(map[string]any)
	if _, ok := providers[ProviderName]; !ok {
		return false, path, nil
	}
	delete(providers, ProviderName)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, path, err
	}
	return true, path, os.WriteFile(path, append(out, '\n'), 0o644)
}

// toModelList 把模型清单转成 pi 的形状（排序保证多次执行结果一致）。
func toModelList(models []ModelEntry) []map[string]any {
	seen := map[string]struct{}{}
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		item := map[string]any{"id": id}
		if name := strings.TrimSpace(m.Name); name != "" && name != id {
			item["name"] = name
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["id"].(string) < out[j]["id"].(string) })
	return out
}

// orDefault 空值回落。
func orDefault(v, fallback string) string {
	if trimmed := strings.TrimSpace(v); trimmed != "" {
		return trimmed
	}
	return fallback
}
