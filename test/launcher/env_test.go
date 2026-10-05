package launcher_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/developstack/aidevstack/cli/internal/launcher"
)

// TestBuildEnvStripsProviderCredentials 厂商密钥、OAuth token、云厂商开关、改道地址一律去掉（只报告名字）；
// 普通变量保留；托管变量以启动器给的值为准。
func TestBuildEnvStripsProviderCredentials(t *testing.T) {
	base := []string{
		"PATH=/usr/bin", "HOME=/home/dev", "GITHUB_TOKEN=keep-me", "LANG=C",
		"ANTHROPIC_API_KEY=sk-ant", "ANTHROPIC_AUTH_TOKEN=x", "ANTHROPIC_BASE_URL=https://evil", "OPENAI_API_KEY=sk",
		"OPENAI_BASE_URL=https://evil", "DEEPSEEK_API_KEY=d", "openrouter_api_key=lower", "AWS_ACCESS_KEY_ID=a",
		"AWS_PROFILE=p", "AZURE_OPENAI_ENDPOINT=e", "CLAUDE_CODE_OAUTH_TOKEN=o", "CLAUDE_CODE_USE_BEDROCK=1",
		"GOOGLE_APPLICATION_CREDENTIALS=/k.json", "HF_TOKEN=h", "PI_CODING_AGENT_DIR=/home/dev/.pi/agent",
		"CLAUDE_CONFIG_DIR=/home/dev/.claude", "AIDEV_TOKEN=stale",
	}
	env, removed := launcher.BuildEnv(base, map[string]string{"AIDEV_TOKEN": "fresh", "PI_CODING_AGENT_DIR": "/managed"})
	joined := strings.Join(env, "\n")
	for _, kept := range []string{
		"PATH=/usr/bin", "HOME=/home/dev", "GITHUB_TOKEN=keep-me", "LANG=C",
		"AIDEV_TOKEN=fresh", "PI_CODING_AGENT_DIR=/managed",
	} {
		if !strings.Contains(joined, kept) {
			t.Errorf("应保留 %s", kept)
		}
	}
	for _, gone := range []string{"sk-ant", "https://evil", "keep-me-not", "/home/dev/.pi/agent", "/home/dev/.claude", "stale"} {
		if strings.Contains(joined, gone) {
			t.Errorf("不应残留 %s", gone)
		}
	}
	if len(removed) != 17 || !slices.IsSorted(removed) || slices.Contains(removed, "GITHUB_TOKEN") {
		t.Fatalf("removed = %v", removed)
	}
}
