package claude_test

import (
	"flag"
	"path/filepath"
	"strings"
	"testing"

	"github.com/developstack/aidevstack/cli/internal/adapter"
	"github.com/developstack/aidevstack/cli/internal/adapter/claude"
	"github.com/developstack/aidevstack/cli/internal/secret"
	"github.com/developstack/aidevstack/cli/test/adaptertest"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestClaudeGoldenFiles 托管 settings.json / mcp.json 逐字节与黄金文件一致；不含秘密。
func TestClaudeGoldenFiles(t *testing.T) {
	files, err := claude.New().Files(adapter.RenderInput{
		Manifest: adaptertest.Manifest(), AgentDir: "/state/agents/p1/claude", CLIVersion: "2.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %d", len(files))
	}
	for _, f := range files {
		if strings.Contains(string(f.Data), adaptertest.Secret) {
			t.Fatalf("%s 含有密钥明文", f.Path)
		}
		adaptertest.Golden(t, filepath.Join("testdata", f.Path+".golden"), f.Data, *update)
	}
}

// TestClaudeNonAnthropicDefaultAndNoMCP 默认模型的上游不是 Anthropic 时关掉实验性 beta；MCP 未开启时 mcp.json 为空
// （配合 --strict-mcp-config 屏蔽仓库的 .mcp.json）。
func TestClaudeNonAnthropicDefaultAndNoMCP(t *testing.T) {
	m := adaptertest.Manifest()
	m.Defaults.Model = "deepseek-chat"
	m.Mcp.Enabled = false
	files, err := claude.New().Files(adapter.RenderInput{Manifest: m, CLIVersion: "2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files[0].Data), `"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1"`) {
		t.Fatalf("settings = %s", files[0].Data)
	}
	// deepseek-chat 的窗口未知（0）：不写上下文上限，交给 Claude Code 的默认。
	if strings.Contains(string(files[0].Data), "CLAUDE_CODE_MAX_CONTEXT_TOKENS") {
		t.Fatalf("窗口未知时不应写上下文上限：%s", files[0].Data)
	}
	if strings.TrimSpace(string(files[1].Data)) != "{\n  \"mcpServers\": {}\n}" {
		t.Fatalf("mcp = %s", files[1].Data)
	}
}

// TestClaudeContextWindowForNonAnthropicDefault 默认模型不是 Anthropic 原生且窗口已知：把窗口写进
// CLAUDE_CODE_MAX_CONTEXT_TOKENS（否则 Claude Code 按 200k 管 auto-compact，128k 的模型会先超窗）；
// Anthropic 原生的默认模型不写（Claude Code 自己认识它的窗口）。
func TestClaudeContextWindowForNonAnthropicDefault(t *testing.T) {
	m := adaptertest.Manifest()
	m.Defaults.Model = "gpt-5"
	files, err := claude.New().Files(adapter.RenderInput{Manifest: m, CLIVersion: "2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files[0].Data), `"CLAUDE_CODE_MAX_CONTEXT_TOKENS": "400000"`) {
		t.Fatalf("settings = %s", files[0].Data)
	}
	files, err = claude.New().Files(adapter.RenderInput{Manifest: adaptertest.Manifest(), CLIVersion: "2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(files[0].Data), "CLAUDE_CODE_MAX_CONTEXT_TOKENS") {
		t.Fatalf("Anthropic 原生默认模型不应写上下文上限：%s", files[0].Data)
	}
}

// TestClaudeLaunch 进程环境只有配置目录与秘密；不信任仓库配置时 --setting-sources user + --strict-mcp-config；
// 与托管冲突的参数拒绝；不传 bypassPermissions。
func TestClaudeLaunch(t *testing.T) {
	in := adapter.LaunchInput{
		Manifest: adaptertest.Manifest(), AgentDir: "/state/agents/p1/claude", Secret: secret.New(adaptertest.Secret),
		Passthrough: []string{"--continue"},
	}
	got, err := claude.New().Launch(in)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(got.Args, " ")
	want := "--settings /state/agents/p1/claude/settings.json --mcp-config /state/agents/p1/claude/mcp.json " +
		"--setting-sources user --strict-mcp-config --continue"
	if args != filepath.FromSlash(want) && args != want {
		t.Fatalf("args = %s", args)
	}
	if strings.Contains(args, "bypassPermissions") || len(got.Env) != 3 ||
		got.Env["ANTHROPIC_AUTH_TOKEN"] != adaptertest.Secret || got.Env["AIDEV_MCP_TOKEN"] != adaptertest.Secret ||
		got.Env["CLAUDE_CONFIG_DIR"] != "/state/agents/p1/claude" {
		t.Fatalf("env = %v", got.Env)
	}
	for _, bad := range [][]string{{"--settings", "x"}, {"--mcp-config=x"}, {"--setting-sources", "project"}} {
		in.Passthrough = bad
		if _, err := claude.New().Launch(in); err == nil {
			t.Errorf("透传 %v 应被拒绝", bad)
		}
	}
	in.Manifest.Policy.TrustRepoAgentConfig, in.Passthrough = true, nil
	if got, _ := claude.New().Launch(in); strings.Contains(strings.Join(got.Args, " "), "--setting-sources") {
		t.Fatalf("信任仓库配置时不限制设置来源：%v", got.Args)
	}
	if v, ok := claude.New().ParseVersion("2.1.285 (Claude Code)\n"); !ok || v != "2.1.285" {
		t.Fatalf("version = %q", v)
	}
}

// TestClaudeRouteOptionSkipsDefault 默认模型本身就是路由时不再占用唯一的自定义选项位；没有别的路由就不写。
func TestClaudeRouteOptionSkipsDefault(t *testing.T) {
	m := adaptertest.Manifest()
	m.Defaults.Model = "project-default"
	files, err := claude.New().Files(adapter.RenderInput{Manifest: m, CLIVersion: "2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	settings := string(files[0].Data)
	if strings.Contains(settings, "ANTHROPIC_CUSTOM_MODEL_OPTION") ||
		!strings.Contains(settings, `"ANTHROPIC_MODEL": "project-default"`) {
		t.Fatalf("settings = %s", settings)
	}
}
