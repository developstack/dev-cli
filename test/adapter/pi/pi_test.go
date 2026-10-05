package pi_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/developstack/aidevstack/cli/internal/adapter"
	"github.com/developstack/aidevstack/cli/internal/adapter/pi"
	"github.com/developstack/aidevstack/cli/internal/api"
	"github.com/developstack/aidevstack/cli/internal/secret"
	"github.com/developstack/aidevstack/cli/test/adaptertest"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestPiGoldenFiles 托管文件逐字节与黄金文件一致（go test ./test/adapter/pi -update 重新生成）；文件里不含任何秘密。
func TestPiGoldenFiles(t *testing.T) {
	files, err := pi.New().Files(adapter.RenderInput{
		Manifest: adaptertest.Manifest(), AgentDir: "/state/agents/p1/pi", CLIVersion: "2.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Path)
		if f.Mode != 0o600 {
			t.Errorf("%s mode = %v", f.Path, f.Mode)
		}
		if strings.Contains(string(f.Data), adaptertest.Secret) {
			t.Fatalf("%s 含有密钥明文", f.Path)
		}
		adaptertest.Golden(t, filepath.Join("testdata", f.Path+".golden"), f.Data, *update)
	}
	if strings.Join(names, ",") != "models.json,settings.json,mcp.json" {
		t.Fatalf("files = %v", names)
	}
}

// TestPiLaunch 启动参数：-ns + 每个技能 --skill + -na（默认不信任仓库配置）+ 透传；环境变量指向托管目录与密钥；
// 与托管冲突的透传参数被拒绝；信任仓库配置时改用 -a。
func TestPiLaunch(t *testing.T) {
	m := adaptertest.Manifest()
	in := adapter.LaunchInput{
		Manifest: m, AgentDir: "/state/agents/p1/pi", Secret: secret.New(adaptertest.Secret), Passthrough: []string{"-c"},
	}
	got, err := pi.New().Launch(in)
	if err != nil {
		t.Fatal(err)
	}
	want := "-ns --skill /state/agents/p1/pi/skills/code-review -na -c"
	if strings.Join(got.Args, " ") != filepath.FromSlash(want) && strings.Join(got.Args, " ") != want {
		t.Fatalf("args = %v", got.Args)
	}
	if got.Env["PI_CODING_AGENT_DIR"] != "/state/agents/p1/pi" || got.Env["AIDEV_TOKEN"] != adaptertest.Secret ||
		got.Env["AIDEV_MCP_TOKEN"] != adaptertest.Secret || got.Env["PI_TELEMETRY"] != "0" {
		t.Fatalf("env = %v", got.Env)
	}
	for _, bad := range [][]string{{"--api-key", "x"}, {"--provider=openai"}, {"-a"}} {
		in.Passthrough = bad
		if _, err := pi.New().Launch(in); err == nil {
			t.Errorf("透传 %v 应被拒绝", bad)
		}
	}
	in.Manifest.Policy.TrustRepoAgentConfig = true
	in.Passthrough = []string{"-a"}
	if got, err := pi.New().Launch(in); err != nil || !strings.Contains(strings.Join(got.Args, " "), " -a") ||
		strings.Contains(strings.Join(got.Args, " "), "-na") {
		t.Fatalf("信任仓库配置时 = %v %v", got.Args, err)
	}
}

// TestPiVersionAndForbidden 版本解析与每次删除的自带凭据文件。
func TestPiVersionAndForbidden(t *testing.T) {
	if v, ok := pi.New().ParseVersion("1.0.0\n"); !ok || v != "1.0.0" {
		t.Fatalf("version = %q", v)
	}
	if f := pi.New().Forbidden(); len(f) != 1 || f[0] != "auth.json" {
		t.Fatalf("forbidden = %v", f)
	}
	if pi.New().MinVersion(api.DevenvManifest{}) != "" || !adapter.VersionBelow("0.98.1", "0.99.0") {
		t.Fatal("min version")
	}
	_ = os.Getenv
}
