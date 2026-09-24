package agents

import "testing"

// TestGatewayBaseHandlesProtocolRoot 两种协议的"根"不一样，不能混用。
//
// 学习点（踩过）：Claude Code 的 `ANTHROPIC_BASE_URL` 只到域名（它自己拼 `/v1/messages`），
// 传成 `.../v1` 会变成 `/v1/v1/messages`；而 pi 的 OpenAI 形状 base 必须含 `/v1`。
// 平台的配置项是个自由文本，所以这里对两种写法都要健壮。
func TestGatewayBaseHandlesProtocolRoot(t *testing.T) {
	claude, _ := Lookup("claude")
	pi, _ := Lookup("pi")

	cases := []struct {
		name, agentID, input, want string
	}{
		{"Claude 去掉 /v1", "claude", "http://host:8080/v1", "http://host:8080"},
		{"Claude 无 /v1 时原样", "claude", "http://host:8080", "http://host:8080"},
		{"Claude 去尾斜杠", "claude", "http://host:8080/v1/", "http://host:8080"},
		{"pi 补齐 /v1", "pi", "http://host:8080", "http://host:8080/v1"},
		{"pi 已有 /v1 保持", "pi", "http://host:8080/v1", "http://host:8080/v1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := claude
			if tc.agentID == "pi" {
				agent = pi
			}
			if got := agent.GatewayBase(tc.input); got != tc.want {
				t.Fatalf("GatewayBase(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestEnvForUsesToolSpecificNames 各工具认的环境变量名不同 —— 差异在这里吃掉。
func TestEnvForUsesToolSpecificNames(t *testing.T) {
	claude, _ := Lookup("claude")
	pi, _ := Lookup("pi")

	claudeEnv := claude.EnvFor("http://host", "k1")
	if len(claudeEnv) != 2 || claudeEnv[0] != "ANTHROPIC_BASE_URL=http://host" ||
		claudeEnv[1] != "ANTHROPIC_AUTH_TOKEN=k1" {
		t.Fatalf("claude env = %v", claudeEnv)
	}
	piEnv := pi.EnvFor("http://host/v1", "k2")
	if len(piEnv) != 2 || piEnv[0] != "DEV_CLI_GATEWAY_URL=http://host/v1" ||
		piEnv[1] != "DEV_CLI_GATEWAY_KEY=k2" {
		t.Fatalf("pi env = %v", piEnv)
	}
}

// TestClaudeDefaultArgs 默认带 bypassPermissions（用户可追加别的参数）。
func TestClaudeDefaultArgs(t *testing.T) {
	claude, ok := Lookup("claude")
	if !ok {
		t.Fatal("Lookup(claude) 失败")
	}
	if len(claude.DefaultArgs) != 2 || claude.DefaultArgs[0] != "--permission-mode" ||
		claude.DefaultArgs[1] != "bypassPermissions" {
		t.Fatalf("DefaultArgs = %v", claude.DefaultArgs)
	}
}
