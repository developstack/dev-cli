package auth_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/developstack/aidevstack/cli/internal/api"
	"github.com/developstack/aidevstack/cli/internal/auth"
	"github.com/developstack/aidevstack/cli/test/fakeplatform"
)

// noSleep 记录每次等待的时长，不真等。
type noSleep struct{ waits []time.Duration }

func (n *noSleep) sleep(_ context.Context, d time.Duration) error {
	n.waits = append(n.waits, d)
	return nil
}

// TestLoginOpensBrowserAndPolls 打开 verification_uri_complete、显示 user_code、pending 时继续轮询、批准后返回凭据。
func TestLoginOpensBrowserAndPolls(t *testing.T) {
	t.Setenv("AIDEVSTACK_NO_BROWSER", "")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_TTY", "")
	t.Setenv("DISPLAY", ":0")
	p := fakeplatform.New(t)
	p.ApproveAfter = 3
	var out bytes.Buffer
	var opened string
	sleeper := &noSleep{}
	cred, err := auth.Login(context.Background(), api.New(p.URL()), auth.Options{
		Out: &out, OpenBrowser: func(u string) error { opened = u; return nil }, Sleep: sleeper.sleep,
	})
	if err != nil {
		t.Fatal(err)
	}
	if opened != p.URL()+"/device?user_code=BCDF-GHJK" || !strings.Contains(out.String(), "BCDF-GHJK") {
		t.Fatalf("opened=%q out=%q", opened, out.String())
	}
	if len(sleeper.waits) != 3 || sleeper.waits[0] != time.Second {
		t.Fatalf("应按 interval 轮询 3 次：%v", sleeper.waits)
	}
	if !strings.HasPrefix(cred.AccessToken.Reveal(), "adsk_cli_") || cred.DeviceID == "" || cred.UserName != "Dev" ||
		cred.Platform != p.URL() || time.Until(cred.ExpiresAt) < 6*24*time.Hour {
		t.Fatalf("cred = %+v", cred)
	}
}

// TestLoginNoBrowserAndDenied --no-browser 只打印 URL；用户拒绝 → ErrDenied。
func TestLoginNoBrowserAndDenied(t *testing.T) {
	p := fakeplatform.New(t)
	p.Deny = true
	var out bytes.Buffer
	_, err := auth.Login(context.Background(), api.New(p.URL()), auth.Options{
		Out: &out, NoBrowser: true, Sleep: (&noSleep{}).sleep,
		OpenBrowser: func(string) error { t.Fatal("--no-browser 不应打开浏览器"); return nil },
	})
	if !errors.Is(err, auth.ErrDenied) || !strings.Contains(out.String(), p.URL()+"/device") {
		t.Fatalf("err=%v out=%q", err, out.String())
	}
}

// TestLoginCancelled Ctrl-C（context 取消）立即返回。
func TestLoginCancelled(t *testing.T) {
	p := fakeplatform.New(t)
	p.ApproveAfter = 100
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := auth.Login(ctx, api.New(p.URL()), auth.Options{NoBrowser: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
