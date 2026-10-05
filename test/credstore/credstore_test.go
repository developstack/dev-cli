package credstore_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/developstack/aidevstack/cli/internal/credstore"
	"github.com/developstack/aidevstack/cli/internal/secret"
)

func sample(platform string) credstore.Credential {
	return credstore.Credential{
		Platform: platform, AccessToken: secret.New("adsk_cli_" + strings.Repeat("a", 40)), DeviceID: "cli_1",
		UserID: "u1", UserName: "Dev", ExpiresAt: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC),
	}
}

// exercise 是两种存储共用的契约：保存 → 读回 → 按平台隔离 → 删除（删除不存在的不报错）。
func exercise(t *testing.T, store credstore.Store) {
	t.Helper()
	a, b := sample("https://a.example.com"), sample("https://b.example.com")
	b.DeviceID = "cli_2"
	for _, c := range []credstore.Credential{a, b} {
		if err := store.Save(c); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.Load(a.Platform)
	if err != nil || got.AccessToken.Reveal() != a.AccessToken.Reveal() || got.DeviceID != "cli_1" ||
		!got.ExpiresAt.Equal(a.ExpiresAt) || got.UserName != "Dev" {
		t.Fatalf("Load = %+v %v", got, err)
	}
	if err := store.Delete(a.Platform); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(a.Platform); !errors.Is(err, credstore.ErrNotFound) {
		t.Fatalf("删除后应 ErrNotFound：%v", err)
	}
	if got, err := store.Load(b.Platform); err != nil || got.DeviceID != "cli_2" {
		t.Fatalf("另一个平台不受影响：%+v %v", got, err)
	}
	if err := store.Delete(a.Platform); err != nil {
		t.Fatalf("重复删除不应报错：%v", err)
	}
}

// TestFileStore 回退文件：0600、目录 0700，内容可读回。
func TestFileStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "credentials.json")
	store := credstore.NewFileStore(path)
	exercise(t, store)
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("凭据文件权限 = %v %v", info.Mode(), err)
	}
	dir, _ := os.Stat(filepath.Dir(path))
	if dir.Mode().Perm() != 0o700 {
		t.Fatalf("目录权限 = %v", dir.Mode())
	}
}

// TestKeychainStore 钥匙串（go-keyring 的内存替身）。
func TestKeychainStore(t *testing.T) {
	keyring.MockInit()
	store := credstore.NewKeychainStore()
	if err := store.Probe(); err != nil {
		t.Fatal(err)
	}
	exercise(t, store)
}

// TestOpenSelectsStore AIDEVSTACK_CREDENTIAL_STORE=file 强制文件；钥匙串可用时用钥匙串；不可用时回退并告警。
func TestOpenSelectsStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv("AIDEVSTACK_CREDENTIAL_STORE", "file")
	if store, warning := credstore.Open(path); store.Kind() != "file" || warning != "" {
		t.Fatalf("强制文件 = %s %q", store.Kind(), warning)
	}
	t.Setenv("AIDEVSTACK_CREDENTIAL_STORE", "")
	keyring.MockInit()
	if store, warning := credstore.Open(path); store.Kind() != "keychain" || warning != "" {
		t.Fatalf("钥匙串可用 = %s %q", store.Kind(), warning)
	}
	keyring.MockInitWithError(os.ErrPermission)
	store, warning := credstore.Open(path)
	if store.Kind() != "file" || !strings.Contains(warning, path) {
		t.Fatalf("钥匙串不可用应回退文件并告警：%s %q", store.Kind(), warning)
	}
}

// TestExpired 过期判断留 1 分钟余量；零值视为过期。
func TestExpired(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	c := credstore.Credential{ExpiresAt: now.Add(30 * time.Second)}
	if !c.Expired(now) || (credstore.Credential{}).Expired(now) == false {
		t.Fatal("30 秒后过期与零值都应视为已过期")
	}
	if (credstore.Credential{ExpiresAt: now.Add(time.Hour)}).Expired(now) {
		t.Fatal("1 小时后过期应视为有效")
	}
}
