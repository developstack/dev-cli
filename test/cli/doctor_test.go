package cli_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/developstack/aidevstack/cli/internal/workspace"
)

// TestDoctor 同步之后逐项诊断：仓库、忽略规则、绑定、平台可达与凭据有效、环境里的厂商凭据（只列名字）、agent 版本；
// claude 没装只告警（退出码 0）；凭证文件权限过宽是 FAIL（退出码 1）。
func TestDoctor(t *testing.T) {
	h := newRepoHarness(t)
	h.t.Setenv("AIDEVSTACK_PLATFORM", h.platform.URL())
	if code, _, errOut := h.start("sync", "--agent", "pi"); code != 0 {
		t.Fatalf("sync = %d %s", code, errOut)
	}
	h.lookPath = func(name string) (string, error) {
		if name == "pi" {
			return h.agent, nil
		}
		return "", errors.New("not found")
	}
	code, out, errOut := h.start("doctor")
	for _, want := range []string{
		"[ok  ] repository " + h.root, "[ok  ] .aidevstack/credentials.json is ignored by git",
		"[ok  ] bound to project Payments (p1)", "is reachable", "[ok  ] signed in as Dev",
		"[warn] provider credentials in your environment are removed when dev-cli starts an agent: " +
			"ANTHROPIC_API_KEY, OPENAI_API_KEY",
		"[ok  ] pi 1.0.0", "[warn] claude: not installed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor 缺少 %q：\n%s%s", want, out, errOut)
		}
	}
	if code != 0 {
		t.Fatalf("没有 FAIL 时退出码应为 0：%d\n%s", code, out)
	}
	if strings.Contains(out, "sk-ant-own") || strings.Contains(out, "adsk_dev_secret") {
		t.Fatal("doctor 不得打印秘密")
	}
	if err := os.Chmod(workspace.CredentialsPath(h.root), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := h.start("doctor"); code != 1 || !strings.Contains(out, "readable by other users") {
		t.Fatalf("权限过宽应 FAIL：%d\n%s", code, out)
	}
}
