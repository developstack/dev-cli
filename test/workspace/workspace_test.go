package workspace_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/developstack/aidevstack/cli/internal/secret"
	"github.com/developstack/aidevstack/cli/internal/workspace"
)

// TestCredentialsFile 凭证 0600、目录 0700，读回一致；续签判定看平台、项目、到期与续签窗口。
func TestCredentialsFile(t *testing.T) {
	root := t.TempDir()
	ws := workspace.Open(root)
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	creds := workspace.Credentials{
		Platform: "https://ai.example.com", ProjectID: "p1", VirtualKeyID: "vk-1", CredentialID: "vkc-1",
		Prefix: "adsk_dev_abc", Secret: secret.New("adsk_dev_secret"), ExpiresAt: now.Add(7 * 24 * time.Hour),
		RenewBefore: 48 * time.Hour,
	}
	if err := ws.SaveCredentials(creds); err != nil {
		t.Fatal(err)
	}
	got, found, err := ws.LoadCredentials()
	if err != nil || !found || got.Secret.Reveal() != "adsk_dev_secret" || got.RenewBefore != 48*time.Hour ||
		!got.ExpiresAt.Equal(creds.ExpiresAt) {
		t.Fatalf("LoadCredentials = %+v %v %v", got, found, err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(workspace.CredentialsPath(root))
		dir, _ := os.Stat(workspace.Dir(root))
		if info.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
			t.Fatalf("perm file=%v dir=%v", info.Mode(), dir.Mode())
		}
	}
	cases := []struct {
		name     string
		platform string
		project  string
		at       time.Time
		want     bool
	}{
		{"仍然有效", "https://ai.example.com", "p1", now, false},
		{"进入续签窗口", "https://ai.example.com", "p1", now.Add(5*24*time.Hour + time.Minute), true},
		{"换了项目", "https://ai.example.com", "p2", now, true},
		{"换了平台", "https://other.example.com", "p1", now, true},
	}
	for _, c := range cases {
		if got := creds.NeedsRenewal(c.platform, c.project, c.at); got != c.want {
			t.Errorf("%s：NeedsRenewal = %v", c.name, got)
		}
	}
	if !(workspace.Credentials{}).NeedsRenewal("x", "p1", now) {
		t.Fatal("没有凭证必须签发")
	}
}

// TestLockSerializes 同一仓库第二个进程等锁；释放后可再取。
func TestLockSerializes(t *testing.T) {
	ws := workspace.Open(t.TempDir())
	first, err := ws.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := ws.Lock(ctx); !errors.Is(err, workspace.ErrLocked) {
		t.Fatalf("持锁期间应 ErrLocked：%v", err)
	}
	first.Unlock()
	second, err := ws.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second.Unlock()
	if _, err := os.Stat(filepath.Join(workspace.Dir(ws.Root), ".lock")); err != nil {
		t.Fatal(err)
	}
}

// TestIndexRemovesCredentials 工作区索引幂等，logout --all 删除各仓库的凭证文件。
func TestIndexRemovesCredentials(t *testing.T) {
	index := workspace.NewIndex(filepath.Join(t.TempDir(), "workspaces.json"))
	a, b := t.TempDir(), t.TempDir()
	for _, root := range []string{a, b, a} {
		if err := index.Add(root); err != nil {
			t.Fatal(err)
		}
	}
	if err := workspace.Open(a).SaveCredentials(workspace.Credentials{Secret: secret.New("x")}); err != nil {
		t.Fatal(err)
	}
	roots, _ := index.Roots()
	removed, err := index.RemoveAllCredentials()
	if err != nil || removed != 1 || len(roots) != 2 {
		t.Fatalf("removed=%d roots=%v err=%v", removed, roots, err)
	}
}
