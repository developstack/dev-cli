package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/developstack/aidevstack/cli/internal/api"
	"github.com/developstack/aidevstack/cli/internal/cli"
	"github.com/developstack/aidevstack/cli/internal/workspace"
	"github.com/developstack/aidevstack/cli/test/adaptertest"
	"github.com/developstack/aidevstack/cli/test/fakeplatform"
	"github.com/developstack/aidevstack/cli/test/gittest"
)

// launched 记录一次 exec（代替替换进程）。
type launched struct {
	binary string
	argv   []string
	env    []string
}

// repoHarness 是在一个登记过的仓库里运行 dev-cli 的环境。
type repoHarness struct {
	*harness
	root  string
	agent string
	execs []launched
	// lookPath 替换 PATH 查找（nil = 所有 agent 都解析到假 agent 脚本）。
	lookPath func(string) (string, error)
}

func newRepoHarness(t *testing.T) *repoHarness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("假 agent 用 shell 脚本")
	}
	h := &repoHarness{harness: newHarness(t), root: gittest.Init(t, "git@github.com:Acme/Payments.git")}
	h.platform.Remotes["github.com/acme/payments"] = api.DevenvProject{ID: "p1", Name: "Payments", TeamName: "Team A"}
	m := adaptertest.Manifest()
	m.Gateway.OpenaiBaseURL, m.Gateway.AnthropicBaseURL = h.platform.URL()+"/v1", h.platform.URL()
	bundle, digest := adaptertest.SkillBundle(t, "code-review", map[string]string{"SKILL.md": "# review\n"})
	m.Skills[0].Digest, m.Skills[0].Size = digest, int64(len(bundle))
	h.platform.Bundles[m.Skills[0].BundleURL] = bundle
	h.platform.Manifest["p1"] = m
	h.agent = filepath.Join(t.TempDir(), "pi")
	if err := os.WriteFile(h.agent, []byte("#!/bin/sh\necho 1.0.0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return h
}

// start 在仓库里执行一次 dev-cli（exec 被记录而不是替换进程）。
func (h *repoHarness) start(args ...string) (int, string, string) {
	h.t.Helper()
	var out, errOut bytes.Buffer
	app := &cli.App{
		In: strings.NewReader(""), Out: &out, Err: &errOut, Dirs: h.dirs, Store: h.store,
		OpenBrowser: func(string) error { return nil },
		LoginSleep:  func(context.Context, time.Duration) error { return nil },
		Getwd:       func() (string, error) { return h.root, nil },
		LookPath:    h.look,
		Environ: func() []string {
			return []string{"PATH=/usr/bin", "HOME=/home/dev", "ANTHROPIC_API_KEY=sk-ant-own", "OPENAI_API_KEY=sk-own"}
		},
		Exec: func(binary string, argv, env []string) error {
			h.execs = append(h.execs, launched{binary: binary, argv: argv, env: env})
			return nil
		},
	}
	code := app.Run(context.Background(), args)
	return code, out.String(), errOut.String()
}

func (h *repoHarness) look(name string) (string, error) {
	if h.lookPath != nil {
		return h.lookPath(name)
	}
	return h.agent, nil
}

func envValue(env []string, name string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

// TestStartPiEndToEnd 首次 start：自动登录 → 解析仓库 → 写 .gitignore 托管块 → 签发开发者密钥（0600）→ 拉清单 →
// 渲染 pi 托管目录 → 去掉自带厂商密钥后 exec pi；第二次 start 不再 resolve、凭证经平台确认仍有效不重签、清单 304。
func TestStartPiEndToEnd(t *testing.T) {
	h := newRepoHarness(t)
	h.t.Setenv("AIDEVSTACK_PLATFORM", h.platform.URL())
	code, _, errOut := h.start("start", "pi", "--", "-c")
	if code != 0 || len(h.execs) != 1 {
		t.Fatalf("start = %d %s", code, errOut)
	}
	exec := h.execs[0]
	agentDir := h.dirs.AgentDir("p1", "pi")
	if exec.binary != h.agent || strings.Join(exec.argv[1:], " ") !=
		"-ns --skill "+filepath.Join(agentDir, "skills", "code-review")+" -na -c" {
		t.Fatalf("argv = %v", exec.argv)
	}
	// 技能已按 digest 校验后装进托管目录（--skill 指向的就是它）。
	if got, err := os.ReadFile(filepath.Join(agentDir, "skills", "code-review", "SKILL.md")); err != nil ||
		string(got) != "# review\n" {
		t.Fatalf("托管技能 = %q %v", got, err)
	}
	creds, found, err := workspace.Open(h.root).LoadCredentials()
	if err != nil || !found || !strings.HasPrefix(creds.Secret.Reveal(), "adsk_dev_") {
		t.Fatalf("credentials = %+v %v %v", creds, found, err)
	}
	if token, _ := envValue(exec.env, "AIDEV_TOKEN"); token != creds.Secret.Reveal() {
		t.Fatalf("AIDEV_TOKEN = %q", token)
	}
	if dir, _ := envValue(exec.env, "PI_CODING_AGENT_DIR"); dir != agentDir {
		t.Fatalf("PI_CODING_AGENT_DIR = %q", dir)
	}
	for _, leaked := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
		if _, ok := envValue(exec.env, leaked); ok {
			t.Fatalf("%s 应从 agent 环境里去掉", leaked)
		}
	}
	if !strings.Contains(errOut, "Removed 2 provider credential variable(s)") {
		t.Fatalf("stderr = %s", errOut)
	}
	for _, name := range []string{"models.json", "settings.json", "mcp.json"} {
		if _, err := os.Stat(filepath.Join(agentDir, name)); err != nil {
			t.Fatalf("托管文件 %s：%v", name, err)
		}
	}
	gitignore, _ := os.ReadFile(filepath.Join(h.root, ".gitignore"))
	if !strings.Contains(string(gitignore), "/.aidevstack/") {
		t.Fatalf(".gitignore = %q", gitignore)
	}
	if out := gittest.Run(t, h.root, "status", "--porcelain"); strings.Contains(out, ".aidevstack") {
		t.Fatalf(".aidevstack 不应出现在 git status 里：%s", out)
	}

	before := requestCount(h.platform)
	if code, _, errOut := h.start("start", "pi"); code != 0 {
		t.Fatalf("second start = %d %s", code, errOut)
	}
	ensures := 0
	h.platform.Snapshot(func(p *fakeplatform.Platform) {
		for _, r := range p.Requests[before:] {
			if strings.HasSuffix(r.Target, "/resolve") {
				t.Errorf("第二次 start 不应再 %s", r.Target)
			}
			if strings.HasSuffix(r.Target, "/developer-key") {
				ensures++
			}
		}
	})
	// 每次都向平台确认凭证（看得到服务端吊销），但仍有效时平台回 valid、不签发：本地凭证不变。
	again, _, _ := workspace.Open(h.root).LoadCredentials()
	if ensures != 1 || again.CredentialID != creds.CredentialID || again.Secret.Reveal() != creds.Secret.Reveal() {
		t.Fatalf("第二次 start：ensure %d 次，凭证 %s → %s（期望确认 1 次、凭证不变）", ensures, creds.CredentialID, again.CredentialID)
	}
}

// TestStartRenewsRevokedCredential 本地凭证还没到期、但平台已吊销（设备吊销 / 管理员停用后重新启用）：
// 下一次 start 立即签出新凭证，而不是带着吊销的凭证启动 agent。
func TestStartRenewsRevokedCredential(t *testing.T) {
	h := newRepoHarness(t)
	h.t.Setenv("AIDEVSTACK_PLATFORM", h.platform.URL())
	if code, _, errOut := h.start("start", "pi"); code != 0 {
		t.Fatalf("start = %d %s", code, errOut)
	}
	first, _, _ := workspace.Open(h.root).LoadCredentials()
	h.platform.Snapshot(func(p *fakeplatform.Platform) { delete(p.Issued, first.CredentialID) })
	if code, _, errOut := h.start("start", "pi"); code != 0 {
		t.Fatalf("second start = %d %s", code, errOut)
	}
	renewed, _, _ := workspace.Open(h.root).LoadCredentials()
	if renewed.CredentialID == first.CredentialID {
		t.Fatal("平台吊销后应签出新凭证")
	}
	if token, _ := envValue(h.execs[1].env, "AIDEV_TOKEN"); token != renewed.Secret.Reveal() {
		t.Fatal("agent 应拿到新凭证")
	}
}

// TestStartDisabledDeveloperKey 管理员停用了开发者密钥：start 拒绝并说明原因，不启动 agent。
func TestStartDisabledDeveloperKey(t *testing.T) {
	h := newRepoHarness(t)
	h.t.Setenv("AIDEVSTACK_PLATFORM", h.platform.URL())
	if code, _, errOut := h.start("start", "pi"); code != 0 {
		t.Fatalf("start = %d %s", code, errOut)
	}
	h.platform.Snapshot(func(p *fakeplatform.Platform) { p.DeveloperKeyDisabled = true })
	code, _, errOut := h.start("start", "pi")
	if code == 0 || len(h.execs) != 1 || !strings.Contains(errOut, "disabled by an administrator") {
		t.Fatalf("start = %d（exec %d 次）%s", code, len(h.execs), errOut)
	}
}

// TestStartOfflineUsesFreshCredential 平台不可达：本地凭证仍新鲜且清单有缓存时，--use-cache 照常启动（离线降级）。
func TestStartOfflineUsesFreshCredential(t *testing.T) {
	h := newRepoHarness(t)
	h.t.Setenv("AIDEVSTACK_PLATFORM", h.platform.URL())
	if code, _, errOut := h.start("start", "pi"); code != 0 {
		t.Fatalf("start = %d %s", code, errOut)
	}
	h.platform.Server.Close()
	code, _, errOut := h.start("start", "--use-cache", "pi")
	if code != 0 || len(h.execs) != 2 || !strings.Contains(errOut, "platform unreachable") {
		t.Fatalf("离线 start = %d（exec %d 次）%s", code, len(h.execs), errOut)
	}
}

func requestCount(p *fakeplatform.Platform) int {
	n := 0
	p.Snapshot(func(p *fakeplatform.Platform) { n = len(p.Requests) })
	return n
}

// TestStartRestoresTamperedConfigAndRemovesAuth 托管文件被改 → 告警并恢复；托管目录里的 auth.json → 删除并告警；
// status 报告完整性。
func TestStartRestoresTamperedConfigAndRemovesAuth(t *testing.T) {
	h := newRepoHarness(t)
	h.t.Setenv("AIDEVSTACK_PLATFORM", h.platform.URL())
	if code, _, errOut := h.start("sync", "--agent", "pi"); code != 0 {
		t.Fatalf("sync = %d %s", code, errOut)
	}
	agentDir := h.dirs.AgentDir("p1", "pi")
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{"providers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(`{"anthropic":{"type":"api_key","key":"sk-own"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := h.start("status"); code != 0 || !strings.Contains(out, "modified: models.json") ||
		!strings.Contains(out, "project:    Payments (p1)") || !strings.Contains(out, "pi:         1.0.0") {
		t.Fatalf("status = %d %s", code, out)
	}
	code, _, errOut := h.start("start", "pi")
	if code != 0 || !strings.Contains(errOut, "was modified outside dev-cli") || !strings.Contains(errOut, "removed auth.json") {
		t.Fatalf("start = %d %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("auth.json 应被删除")
	}
	if code, out, _ := h.start("status"); code != 0 || !strings.Contains(out, "managed config intact") {
		t.Fatalf("status = %d %s", code, out)
	}
}

// TestStartRefusals 未登记的仓库给出指引；透传与托管冲突的参数拒绝；agent 版本过低拒绝。
func TestStartRefusals(t *testing.T) {
	h := newRepoHarness(t)
	h.t.Setenv("AIDEVSTACK_PLATFORM", h.platform.URL())
	delete(h.platform.Remotes, "github.com/acme/payments")
	if code, _, errOut := h.start("start", "pi"); code != 1 || !strings.Contains(errOut, "not registered to any project") ||
		!strings.Contains(errOut, "github.com/acme/payments") {
		t.Fatalf("未登记 = %d %s", code, errOut)
	}
	h.platform.Remotes["github.com/acme/payments"] = api.DevenvProject{ID: "p1", Name: "Payments"}
	if code, _, errOut := h.start("start", "pi", "--", "--api-key", "x"); code != 2 || !strings.Contains(errOut, "--api-key") {
		t.Fatalf("冲突参数 = %d %s", code, errOut)
	}
	if err := os.WriteFile(h.agent, []byte("#!/bin/sh\necho 0.98.0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := h.start("start", "pi"); code != 1 || !strings.Contains(errOut, "too old") {
		t.Fatalf("版本过低 = %d %s", code, errOut)
	}
	if len(h.execs) != 0 {
		t.Fatalf("拒绝时不应启动 agent：%v", h.execs)
	}
}

// TestStartNoModelsPointsToConsole 项目模型范围为空（新建项目的安全默认）：不启动 agent，错误说明原因并给出
// 控制台项目页「模型」tab 的链接；平台没给控制台地址时退化为文字指引。
func TestStartNoModelsPointsToConsole(t *testing.T) {
	h := newRepoHarness(t)
	h.t.Setenv("AIDEVSTACK_PLATFORM", h.platform.URL())
	m := h.platform.Manifest["p1"]
	m.Models, m.Defaults.Model = nil, ""
	m.Project.ConsoleURL = "https://console.example.com/app/projects/p1"
	h.platform.Manifest["p1"] = m
	code, _, errOut := h.start("start", "pi")
	if code != 1 || !strings.Contains(errOut, "model scope is empty") ||
		!strings.Contains(errOut, "https://console.example.com/app/projects/p1?tab=models") {
		t.Fatalf("无模型 = %d %s", code, errOut)
	}
	m.Project.ConsoleURL = ""
	h.platform.Manifest["p1"] = m
	if code, _, errOut := h.start("start", "pi"); code != 1 || !strings.Contains(errOut, "project details → Models") {
		t.Fatalf("无控制台地址 = %d %s", code, errOut)
	}
	if len(h.execs) != 0 {
		t.Fatalf("没有模型时不应启动 agent：%v", h.execs)
	}
}
