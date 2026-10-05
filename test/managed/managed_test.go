package managed_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/developstack/aidevstack/cli/internal/managed"
)

// TestSyncTamperForbiddenStale 首次写入；被改动的托管文件报告并覆盖；禁止文件删除；不再需要的旧文件删除；
// agent 自己写的文件不动；路径逃逸拒绝。
func TestSyncTamperForbiddenStale(t *testing.T) {
	dir, state := filepath.Join(t.TempDir(), "agent"), filepath.Join(t.TempDir(), "pi.managed.json")
	files := []managed.File{
		{Path: "models.json", Data: []byte("v1"), Mode: 0o600},
		{Path: "mcp.json", Data: []byte("m"), Mode: 0o600},
	}
	if _, err := managed.Sync(dir, state, files, []string{"auth.json"}); err != nil {
		t.Fatal(err)
	}
	write := func(name, data string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("models.json", "hacked")
	write("auth.json", `{"anthropic":"sk-..."}`)
	write("sessions.jsonl", "agent-owned")
	if tampered, err := managed.Verify(dir, state); err != nil || !slices.Equal(tampered, []string{"models.json"}) {
		t.Fatalf("Verify = %v %v", tampered, err)
	}

	result, err := managed.Sync(dir, state, files[:1], []string{"auth.json"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(result.Tampered, []string{"models.json"}) || !slices.Equal(result.Forbidden, []string{"auth.json"}) ||
		!slices.Equal(result.Removed, []string{"mcp.json"}) {
		t.Fatalf("result = %+v", result)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "models.json"))
	if string(data) != "v1" {
		t.Fatalf("应恢复平台版本：%q", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions.jsonl")); err != nil {
		t.Fatal("agent 自己的文件不应被动")
	}
	if _, err := managed.Sync(dir, state, []managed.File{{Path: "../escape", Data: []byte("x"), Mode: 0o600}}, nil); err == nil {
		t.Fatal("路径逃逸应拒绝")
	}
}

// TestSyncRemovesEmptyForbiddenSilently pi 1.0 每次启动都会在托管目录建空的 auth.json（`{}`）：照样删除，但不报告
// （否则每次 start 都是一条"自带凭据"的误报）；doctor 用的 ForbiddenPresent 同一口径。
func TestSyncRemovesEmptyForbiddenSilently(t *testing.T) {
	dir, state := filepath.Join(t.TempDir(), "agent"), filepath.Join(t.TempDir(), "pi.managed.json")
	files := []managed.File{{Path: "models.json", Data: []byte("v1"), Mode: 0o600}}
	if _, err := managed.Sync(dir, state, files, []string{"auth.json"}); err != nil {
		t.Fatal(err)
	}
	for _, placeholder := range []string{"", "{}", " {}\n", "[]", "null"} {
		if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(placeholder), 0o600); err != nil {
			t.Fatal(err)
		}
		if present, err := managed.ForbiddenPresent(dir, "auth.json"); err != nil || present {
			t.Fatalf("ForbiddenPresent(%q) = %v %v", placeholder, present, err)
		}
		result, err := managed.Sync(dir, state, files, []string{"auth.json"})
		if err != nil || len(result.Forbidden) != 0 {
			t.Fatalf("Sync(%q) = %+v %v", placeholder, result, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "auth.json")); !os.IsNotExist(err) {
			t.Fatalf("占位 auth.json（%q）也应删除", placeholder)
		}
	}
}
