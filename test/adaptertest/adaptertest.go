// Package adaptertest 是适配器测试共用的样例清单与黄金文件工具。
package adaptertest

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/developstack/aidevstack/cli/internal/api"
)

// Secret 是样例开发者密钥（断言托管文件里不出现它）。
//
//nolint:gosec // G101：测试用的假密钥。
const Secret = "adsk_dev_0123456789abcdef0123456789abcdef0123"

// Manifest 返回一份覆盖三种上游协议、有 / 无定价、一个路由别名、MCP 开启、一个技能的样例清单。
func Manifest() api.DevenvManifest {
	var m api.DevenvManifest
	m.SchemaVersion = 1
	m.Project = api.DevenvProject{ID: "p1", Name: "Payments", TeamID: "team-a", TeamName: "Team A"}
	m.Gateway.OpenaiBaseURL = "https://ai.example.com/v1"
	m.Gateway.AnthropicBaseURL = "https://ai.example.com"
	m.Gateway.McpURL = "https://ai.example.com/v1/mcp"
	m.Defaults.Model = "claude-sonnet-5"
	m.Defaults.SmallModel = "claude-haiku-5"
	m.Defaults.ThinkingLevel = "high"
	m.Mcp.Enabled, m.Mcp.ServerName, m.Mcp.ExposureHint = true, "aidevstack", "deferred"
	m.Policy.MinCliVersion = "2.0.0"
	m.Policy.CredentialRenewBeforeSeconds = 172800
	m.Policy.Agents.Pi.MinVersion = "0.99.0"
	m.Policy.Agents.Claude.MinVersion = "2.1.0"
	m.Skills = append(m.Skills, api.DevenvSkill{
		Name: "code-review", Version: "1.0.0", Digest: "sha256:abc", Size: 10,
		BundleURL: "/api/v1/devenv/projects/p1/skills/code-review/1.0.0.zip",
	})
	sonnet := api.DevenvModel{
		ID: "claude-sonnet-5", DisplayName: "Claude Sonnet 5", Kind: "model", Provider: "anthropic",
		Protocols: []api.DevenvModelProtocols{api.AnthropicMessages}, ContextWindow: 200000, MaxOutputTokens: 64000,
		Reasoning: true, Input: []api.DevenvModelInput{api.Text, api.Image}, PromptCache: true,
	}
	sonnet.Cost = &struct {
		CacheRead  float64 `json:"cache_read"`
		CacheWrite float64 `json:"cache_write"`
		Input      float64 `json:"input"`
		Output     float64 `json:"output"`
	}{Input: 3, Output: 15}
	m.Models = []api.DevenvModel{
		sonnet,
		{
			ID: "claude-haiku-5", DisplayName: "Claude Haiku 5", Kind: "model", Provider: "anthropic",
			Protocols: []api.DevenvModelProtocols{api.AnthropicMessages}, Input: []api.DevenvModelInput{api.Text},
		},
		{
			ID: "gpt-5", DisplayName: "GPT-5", Kind: "model", Provider: "openai",
			Protocols: []api.DevenvModelProtocols{api.OpenaiResponses, api.OpenaiCompletions}, ContextWindow: 400000,
			Input: []api.DevenvModelInput{api.Text},
		},
		{
			ID: "deepseek-chat", DisplayName: "deepseek-chat", Kind: "model", Provider: "deepseek",
			Protocols: []api.DevenvModelProtocols{api.OpenaiCompletions}, Input: []api.DevenvModelInput{api.Text},
		},
		{
			// 项目路由别名（ADR-0027）：平台按候选顺序容灾；能力取候选交集，不下发单价。
			ID: "project-default", DisplayName: "project-default（项目路由）", Kind: "route", Provider: "anthropic",
			Protocols: []api.DevenvModelProtocols{api.AnthropicMessages}, ContextWindow: 128000, MaxOutputTokens: 32000,
			Input: []api.DevenvModelInput{api.Text},
		},
	}
	return m
}

// Golden 比对黄金文件；update 为 true 时重写。
func Golden(t *testing.T, path string, got []byte, update bool) {
	t.Helper()
	if update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create)", path, err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Fatalf("%s differs from golden file:\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

// SkillBundle 构造一个技能包（布局 <name>/<path>），返回字节与 `sha256:<hex>` digest。
func SkillBundle(t *testing.T, name string, files map[string]string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for path, content := range files {
		f, err := w.Create(name + "/" + path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), "sha256:" + hex.EncodeToString(sum[:])
}
