package cli

// 工作区命令：init / sync / start / status（设计稿 §10.1）。

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/developstack/aidevstack/cli/internal/adapter"
	"github.com/developstack/aidevstack/cli/internal/adapter/claude"
	"github.com/developstack/aidevstack/cli/internal/adapter/pi"
	"github.com/developstack/aidevstack/cli/internal/launcher"
)

// adapters 是内置的 agent 适配器（pi 优先，设计稿 §9）。
func adapters() map[string]adapter.Adapter {
	return map[string]adapter.Adapter{"pi": pi.New(), "claude": claude.New()}
}

// lookupAdapter 按名字找适配器。
func lookupAdapter(name string) (adapter.Adapter, error) {
	agent, ok := adapters()[name]
	if !ok {
		return nil, usageError{msg: fmt.Sprintf("unknown agent %q (supported: pi, claude)", name)}
	}
	return agent, nil
}

// workspaceFlags 是 init / sync / start 共用的参数。
type workspaceFlags struct {
	commonFlags
	project  string
	useCache bool
}

func (a *App) workspaceFlagSet(name, usage string, wf *workspaceFlags) *flag.FlagSet {
	fs := a.newFlagSet(name, usage)
	wf.register(fs)
	fs.StringVar(&wf.project, "project", "", "project id (when the repository matches several projects)")
	fs.BoolVar(&wf.useCache, "use-cache", false, "if the platform is unreachable, use a manifest cached within 24h")
	return fs
}

// runInit 处理 `dev-cli init [--project ID]`：绑定项目、写 .aidevstack/、维护忽略规则；不签发密钥、不启动 agent。
func runInit(ctx context.Context, a *App, args []string) error {
	var wf workspaceFlags
	fs := a.workspaceFlagSet("init", "init [--project ID] [--platform URL]", &wf)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	out, err := a.syncProject(ctx, syncOptions{flags: wf.commonFlags, project: wf.project, useCache: wf.useCache})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(a.Out, "Bound %s to project %s (%s) via %s %s.\n", out.loc.Root, out.binding.ProjectName,
		out.binding.ProjectID, out.binding.Remote, out.binding.URL)
	return nil
}

// runSync 处理 `dev-cli sync [--agent pi|claude]`：同步凭据、清单与托管配置，不启动（预热 / CI）。
func runSync(ctx context.Context, a *App, args []string) error {
	var wf workspaceFlags
	var agentName string
	fs := a.workspaceFlagSet("sync", "sync [--agent pi|claude] [--project ID]", &wf)
	fs.StringVar(&agentName, "agent", "", "also render this agent's managed config (default: every supported agent)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	agents, err := selectAgents(agentName)
	if err != nil {
		return err
	}
	out, err := a.syncProject(ctx, syncOptions{
		flags: wf.commonFlags, project: wf.project, agents: agents,
		useCache: wf.useCache,
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(a.Out, "Synced project %s (%s): %d models, developer key valid until %s.\n",
		out.binding.ProjectName, out.binding.ProjectID, len(out.manifest.Models),
		out.creds.ExpiresAt.Local().Format(time.DateTime))
	return nil
}

// selectAgents 返回要渲染的适配器（空名字 = 全部）。
func selectAgents(name string) ([]adapter.Adapter, error) {
	if name != "" {
		agent, err := lookupAdapter(name)
		return []adapter.Adapter{agent}, err
	}
	var out []adapter.Adapter
	for _, n := range []string{"pi", "claude"} {
		if agent, ok := adapters()[n]; ok {
			out = append(out, agent)
		}
	}
	return out, nil
}

// runStart 处理 `dev-cli start [flags] <agent> [-- agent args]`。
func runStart(ctx context.Context, a *App, args []string) error {
	var wf workspaceFlags
	fs := a.workspaceFlagSet("start", "start [--project ID] <pi|claude> [-- agent args]", &wf)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	rest := fs.Args()
	name := "pi"
	if len(rest) > 0 && rest[0] != "--" {
		name, rest = rest[0], rest[1:]
	}
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	agent, err := lookupAdapter(name)
	if err != nil {
		return err
	}
	binary, installed, err := a.checkAgent(ctx, agent)
	if err != nil {
		return err
	}
	out, err := a.syncProject(ctx, syncOptions{
		flags: wf.commonFlags, project: wf.project,
		agents: []adapter.Adapter{agent}, useCache: wf.useCache,
	})
	if err != nil {
		return err
	}
	return a.launch(agent, binary, installed, out, rest)
}

// checkAgent 在 PATH 里找 agent 并读版本（版本门槛在拿到清单之后判定）。
func (a *App) checkAgent(ctx context.Context, agent adapter.Adapter) (string, string, error) {
	binary, err := a.LookPath(agent.Binary())
	if err != nil {
		return "", "", fmt.Errorf("%s is not installed or not on PATH; install it with: %s", agent.Binary(),
			agent.UpgradeHint())
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		return binary, "", nil // 读不到版本不阻断（版本门槛只在能判定时生效）
	}
	version, _ := agent.ParseVersion(string(output))
	return binary, version, nil
}

// launch 校验版本与默认模型，组装环境（去掉自带的厂商凭据）并 exec agent。
func (a *App) launch(agent adapter.Adapter, binary, installed string, out syncOutcome, passthrough []string) error {
	if minimum := agent.MinVersion(out.manifest); adapter.VersionBelow(installed, minimum) {
		return fmt.Errorf("%s %s is too old for this platform (need %s or later); upgrade with: %s",
			agent.Binary(), installed, minimum, agent.UpgradeHint())
	}
	if out.manifest.Defaults.Model == "" {
		return noModelsError(out.binding.ProjectName, out.manifest.Project.ConsoleURL)
	}
	dirs, err := a.dirs()
	if err != nil {
		return err
	}
	spec, err := agent.Launch(adapter.LaunchInput{
		Manifest: out.manifest, AgentDir: dirs.AgentDir(out.binding.ProjectID, agent.Name()), Secret: out.creds.Secret,
		Passthrough: passthrough,
	})
	if err != nil {
		return usageError{msg: err.Error()}
	}
	env, removed := launcher.BuildEnv(a.Environ(), spec.Env)
	_, _ = fmt.Fprintf(a.Err, "Starting %s for project %s (default model %s).\n", agent.Name(), out.binding.ProjectName,
		out.manifest.Defaults.Model)
	if len(removed) > 0 {
		_, _ = fmt.Fprintf(a.Err, "Removed %d provider credential variable(s) from the agent's environment: %s\n",
			len(removed), strings.Join(removed, ", "))
	}
	return a.Exec(binary, append([]string{binary}, spec.Args...), env)
}

// noModelsError 是"项目没有可用模型"的指引：项目模型范围为空即全部拒绝（安全默认，ADR-0026 决策 4），
// 只有项目管理员能在控制台配置；链接直达项目详情的「模型」tab（平台给了控制台地址时）。
func noModelsError(project, consoleURL string) error {
	msg := fmt.Sprintf("project %s has no models available: its model scope is empty, "+
		"so every key in the project is denied.\n"+
		"A project admin (platform admin, or the team's group admin) must configure the model scope", project)
	if consoleURL != "" {
		msg += " in the console:\n  " + consoleURL + "?tab=models"
	} else {
		msg += " in the console (project details → Models)."
	}
	return errors.New(msg)
}
