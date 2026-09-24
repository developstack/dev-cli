package piagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withAgentDir 把 pi 的 agent 目录指到临时目录（测试不碰用户真实配置）。
func withAgentDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	return dir
}

// TestEnsureProviderMergesWithoutClobbering 只动我们的键，用户的 provider 原样保留。
//
// 学习点：用户手工配过一堆 provider（deepseek、cc-switch-* 等），整体覆盖会把它们全弄没 ——
// 所以必须"读出来 → 改一个键 → 写回去"。
func TestEnsureProviderMergesWithoutClobbering(t *testing.T) {
	dir := withAgentDir(t)
	existing := `{"providers":{"deepseek":{"baseUrl":"https://api.deepseek.com/v1","apiKey":"sk-x"}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(existing), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := EnsureProvider(Provider{
		BaseURL: "http://host:8080/v1", Models: []ModelEntry{{ID: "b"}, {ID: "a"}, {ID: "a"}},
	}); err != nil {
		t.Fatalf("EnsureProvider() error = %v", err)
	}

	raw, _ := os.ReadFile(filepath.Join(dir, "models.json"))
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	providers, _ := doc["providers"].(map[string]any)
	if _, ok := providers["deepseek"]; !ok {
		t.Fatal("用户的 deepseek provider 被弄没了")
	}
	entry, ok := providers[ProviderName].(map[string]any)
	if !ok {
		t.Fatalf("没写进 %s 条目", ProviderName)
	}
	// 密钥必须是**环境变量引用**，不能是明文。
	if entry["apiKey"] != "$"+EnvAPIKey {
		t.Fatalf("apiKey = %v, want $%s", entry["apiKey"], EnvAPIKey)
	}
	models, _ := entry["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("模型应去重成 2 个，得到 %v", models)
	}
	if !strings.Contains(string(raw), "\"a\"") {
		t.Fatal("模型列表应当写进去")
	}
}

// TestEnsureProviderIsIdempotent 重复执行结果一致（幂等）。
func TestEnsureProviderIsIdempotent(t *testing.T) {
	dir := withAgentDir(t)
	p := Provider{BaseURL: "http://host/v1", Models: []ModelEntry{{ID: "m"}}}

	if _, err := EnsureProvider(p); err != nil {
		t.Fatalf("第一次 error = %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, "models.json"))
	if _, err := EnsureProvider(p); err != nil {
		t.Fatalf("第二次 error = %v", err)
	}
	second, _ := os.ReadFile(filepath.Join(dir, "models.json"))
	if string(first) != string(second) {
		t.Fatalf("两次结果不一致：\n%s\n---\n%s", first, second)
	}
}

// TestRemoveProviderKeepsOthers 撤销只删我们的键。
func TestRemoveProviderKeepsOthers(t *testing.T) {
	dir := withAgentDir(t)
	if err := os.WriteFile(filepath.Join(dir, "models.json"),
		[]byte(`{"providers":{"keep":{"baseUrl":"http://x"}}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := EnsureProvider(Provider{BaseURL: "http://host/v1"}); err != nil {
		t.Fatalf("EnsureProvider: %v", err)
	}

	removed, _, err := RemoveProvider()
	if err != nil || !removed {
		t.Fatalf("RemoveProvider() = (%v, %v), want 删掉一个", removed, err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "models.json"))
	if strings.Contains(string(raw), ProviderName) {
		t.Fatalf("托管条目还在：%s", raw)
	}
	if !strings.Contains(string(raw), "keep") {
		t.Fatalf("用户的 keep 被误删：%s", raw)
	}
}
