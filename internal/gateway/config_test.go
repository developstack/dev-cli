package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestSaveLoadRoundTrip 保存/读回，权限必须是 0600（里面有虚拟密钥）。
func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := Config{BaseURL: "http://host:8080/v1", APIKey: "vk_secret_1234567890", Models: []string{"m1"}}
	if err := Save(root, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(Path(root))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("权限 = %o, want 600（里面有密钥）", perm)
	}

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.APIKey != want.APIKey || got.BaseURL != want.BaseURL {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

// TestLoadMissingIsNotConfigured 没有配置是**正常状态**，用哨兵错误表达。
func TestLoadMissingIsNotConfigured(t *testing.T) {
	root := t.TempDir()
	if _, err := Load(root); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Load() error = %v, want ErrNotConfigured", err)
	}
}

// TestLoadRejectsPartialConfig 字段不全视为"未配置"（避免拿着半份配置去启动 agent）。
func TestLoadRejectsPartialConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".dev-cli"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(Path(root), []byte(`{"base_url":"http://x"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(root); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("缺 api_key 时应视为未配置，得到 %v", err)
	}
}

// TestMaskedNeverLeaksSecret 摘要只露头尾（展示给用户看时不能打全）。
func TestMaskedNeverLeaksSecret(t *testing.T) {
	c := Config{APIKey: "abcdefghijklmnop"}
	masked := c.Masked()
	if masked == c.APIKey {
		t.Fatal("Masked() 不能等于原值")
	}
	if len(masked) >= len(c.APIKey) {
		t.Fatalf("Masked() = %q 太长", masked)
	}
	if short := (Config{APIKey: "abc"}).Masked(); short != "****" {
		t.Fatalf("短密钥应整体打码，得到 %q", short)
	}
}

// TestRemove 删除后回到"未配置"。
func TestRemove(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, Config{BaseURL: "http://x/v1", APIKey: "k"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := Remove(root); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := Load(root); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("删除后应为未配置，得到 %v", err)
	}
	// 再删一次不该报错（幂等）。
	if err := Remove(root); err != nil {
		t.Fatalf("重复 Remove() error = %v", err)
	}
}
