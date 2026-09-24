package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/developstack/dev-cli/internal/agents"
	"github.com/developstack/dev-cli/internal/gateway"
	"github.com/developstack/dev-cli/internal/piagent"
)

// runStart 实现 `dev-cli start <agent> [参数...]`。
//
// 学习点（为什么用"启动器"而不是改工具配置）：
//   - Claude Code 认 `ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN` 环境变量；
//   - pi 认 models.json 里的 provider，而它的 `apiKey` 支持 `$ENV` 插值。
//
// 两条路都能靠**启动时注入环境变量**把流量指到平台 —— 于是**不碰用户的任何配置文件**，
// 虚拟密钥只活在子进程里（不落盘、不污染全局、不用处理 gitignore 冲突）。
func runStart(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(stderr)
	common := parseCommon(fs, os.Getenv)
	noAuth := fs.Bool("no-auth", false, "不注入平台网关（用你本来的登录）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintf(stderr, "用法：dev-cli start <%s> [参数...]\n", strings.Join(agents.IDs(), "|"))
		fmt.Fprintln(stderr, "例如：dev-cli start claude --resume")
		return 2
	}
	agentID, extra := rest[0], rest[1:]

	agent, ok := agents.Lookup(agentID)
	if !ok {
		fmt.Fprintf(stderr, "✗ 不认识 %q；支持：%s\n", agentID, strings.Join(agents.IDs(), "、"))
		return 2
	}

	root, err := projectRoot(common.dir)
	if err != nil {
		return fail(stderr, err)
	}
	if _, err := requireAuth(root); err != nil {
		return fail(stderr, err)
	}

	// 没装就现场问（这是用户明确要的：找不到 agent 要提醒并帮忙装）。
	binary, ready, err := agents.EnsureInstalled(os.Stdin, stdout, agent)
	if err != nil {
		return fail(stderr, err)
	}
	if !ready {
		fmt.Fprintf(stderr, "✗ 没有可用的 %s，先装好再试\n", agent.Name)
		return 1
	}

	env, launchArgs, err := prepareGateway(root, agent, *noAuth, extra, stdout)
	if err != nil {
		return fail(stderr, err)
	}

	fmt.Fprintf(stdout, "→ 启动 %s：%s %s\n", agent.Name, agent.Binary, strings.Join(launchArgs, " "))
	child := exec.CommandContext(ctx, binary, launchArgs...)
	child.Env = env
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, stdout, stderr
	if err := child.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode() // 原样透传 agent 的退出码
		}
		return fail(stderr, err)
	}
	return 0
}

// prepareGateway 组装子进程环境与启动参数（网关未配置时只警告，不阻断启动）。
func prepareGateway(
	root string, agent agents.Agent, noAuth bool, extra []string, stdout io.Writer,
) ([]string, []string, error) {
	launchArgs := append(append([]string{}, agent.DefaultArgs...), extra...)
	env := os.Environ()
	if noAuth {
		return env, launchArgs, nil
	}

	gw, err := gateway.Load(root)
	if errors.Is(err, gateway.ErrNotConfigured) {
		// 学习点：平台没下发网关配置是**正常状态**（管理员没配网关地址）——
		// 如实提示并照常启动，而不是让 `dev-cli start` 直接失败。
		fmt.Fprintln(stdout, "ℹ️  平台尚未下发模型网关配置，本次不注入（将使用你本来的登录）")
		return env, launchArgs, nil
	}
	if err != nil {
		return nil, nil, err
	}

	base := agent.GatewayBase(gw.BaseURL)
	env = append(env, agent.EnvFor(base, gw.APIKey)...)
	fmt.Fprintf(stdout, "🔐 已注入平台网关：%s（密钥 %s）\n", base, gw.Masked())

	// pi 的 provider 只能写在 agent 目录的 models.json —— 补一条**不含密钥**的托管条目。
	if agent.ID == "pi" {
		path, err := piagent.EnsureProvider(piagent.Provider{
			BaseURL: gw.BaseURL, API: "openai-completions", Models: toPIModels(gw.Models),
		})
		if err != nil {
			return nil, nil, err
		}
		fmt.Fprintf(stdout, "   provider %q 已写入 %s（apiKey 用 $%s 引用，明文密钥不落盘）\n",
			piagent.ProviderName, path, piagent.EnvAPIKey)
		if !hasFlag(extra, "--provider") {
			launchArgs = append([]string{"--provider", piagent.ProviderName}, launchArgs...)
		}
		if model := defaultModel(extra, gw.Models); model != "" {
			launchArgs = append([]string{"--model", model}, launchArgs...)
		}
	}
	return env, launchArgs, nil
}

// toPIModels 把平台的模型 id 列表转成 pi 的模型条目。
func toPIModels(models []string) []piagent.ModelEntry {
	out := make([]piagent.ModelEntry, 0, len(models))
	for _, id := range models {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			out = append(out, piagent.ModelEntry{ID: trimmed})
		}
	}
	return out
}

// hasFlag 判断用户是否自己传了这个参数（传了就不替他做决定）。
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name || strings.HasPrefix(a, name+"=") {
			return true
		}
	}
	return false
}

// defaultModel 挑一个默认模型；用户自己传了 --model 就不管。
func defaultModel(extra []string, models []string) string {
	if hasFlag(extra, "--model") || len(models) == 0 {
		return ""
	}
	return strings.TrimSpace(models[0])
}
