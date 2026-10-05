package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/developstack/aidevstack/cli/internal/cli"
	"github.com/developstack/aidevstack/cli/internal/credstore"
	"github.com/developstack/aidevstack/cli/internal/paths"
	"github.com/developstack/aidevstack/cli/test/fakeplatform"
)

// harness 是一次命令行调用的环境：临时用户目录、文件凭据存储、假平台、不真等的登录轮询。
type harness struct {
	t        *testing.T
	platform *fakeplatform.Platform
	dirs     paths.Dirs
	store    credstore.Store
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	t.Setenv("AIDEVSTACK_PLATFORM", "")
	home := t.TempDir()
	dirs := paths.Dirs{Config: home + "/config", State: home + "/state"}
	return &harness{
		t: t, platform: fakeplatform.New(t), dirs: dirs,
		store: credstore.NewFileStore(dirs.CredentialFallbackFile()),
	}
}

// run 执行一次 dev-cli，返回退出码、stdout、stderr。
func (h *harness) run(args ...string) (int, string, string) {
	h.t.Helper()
	var out, errOut bytes.Buffer
	app := &cli.App{
		Out: &out, Err: &errOut, Dirs: h.dirs, Store: h.store,
		OpenBrowser: func(string) error { return nil },
		LoginSleep:  func(context.Context, time.Duration) error { return nil },
	}
	code := app.Run(context.Background(), args)
	return code, out.String(), errOut.String()
}

// TestLoginWhoamiDevicesLogout 登录保存平台地址与凭据 → whoami 校验有效 → devices 标出本机 → 重新登录吊销旧设备 →
// logout 吊销并删除本地凭据。
func TestLoginWhoamiDevicesLogout(t *testing.T) {
	h := newHarness(t)
	code, _, errOut := h.run("login", "--platform", h.platform.URL())
	if code != 0 || !strings.Contains(errOut, "Signed in as Dev") || !strings.Contains(errOut, "BCDF-GHJK") {
		t.Fatalf("login = %d %s", code, errOut)
	}
	code, out, errOut := h.run("whoami")
	if code != 0 || !strings.Contains(out, "status:    valid") || !strings.Contains(out, "stored in: file") {
		t.Fatalf("whoami = %d %s %s", code, out, errOut)
	}
	code, out, _ = h.run("devices")
	if code != 0 || !strings.Contains(out, "(this device)") {
		t.Fatalf("devices = %d %s", code, out)
	}
	first, _ := h.store.Load(h.platform.URL())

	if code, _, errOut := h.run("login"); code != 0 {
		t.Fatalf("relogin = %d %s", code, errOut)
	}
	h.platform.Snapshot(func(p *fakeplatform.Platform) {
		if len(p.Revoked) != 1 || p.Revoked[0] != first.DeviceID {
			t.Fatalf("重新登录应吊销旧设备：%v", p.Revoked)
		}
	})

	code, out, _ = h.run("logout")
	if code != 0 || !strings.Contains(out, "Signed out") {
		t.Fatalf("logout = %d %s", code, out)
	}
	if _, err := h.store.Load(h.platform.URL()); !errors.Is(err, credstore.ErrNotFound) {
		t.Fatalf("logout 后本地凭据应删除：%v", err)
	}
	if code, _, errOut := h.run("whoami"); code != 1 || !strings.Contains(errOut, "not signed in") {
		t.Fatalf("logout 后 whoami = %d %s", code, errOut)
	}
}

// TestDevicesRelogsInOnRevokedCredential 凭据在服务端被吊销（401）→ 自动重新登录一次后继续。
func TestDevicesRelogsInOnRevokedCredential(t *testing.T) {
	h := newHarness(t)
	if code, _, errOut := h.run("login", "--platform", h.platform.URL()); code != 0 {
		t.Fatal(errOut)
	}
	h.platform.Snapshot(func(p *fakeplatform.Platform) {
		for token := range p.Tokens {
			delete(p.Tokens, token)
		}
	})
	code, out, errOut := h.run("devices")
	if code != 0 || !strings.Contains(errOut, "signing in again") || !strings.Contains(out, "(this device)") {
		t.Fatalf("devices = %d %s %s", code, out, errOut)
	}
}

// TestUsageErrors 未知命令与参数错误退出码 2；没有平台地址时提示 login --platform。
func TestUsageErrors(t *testing.T) {
	h := newHarness(t)
	if code, _, _ := h.run("nope"); code != 2 {
		t.Fatalf("未知命令 = %d", code)
	}
	if code, _, _ := h.run("login", "--bogus"); code != 2 {
		t.Fatalf("未知参数 = %d", code)
	}
	if code, _, errOut := h.run("whoami"); code != 1 || !strings.Contains(errOut, "login --platform") {
		t.Fatalf("没有平台 = %d %s", code, errOut)
	}
	if code, out, _ := h.run("version"); code != 0 || !strings.HasPrefix(out, "dev-cli ") {
		t.Fatalf("version = %d %s", code, out)
	}
}
