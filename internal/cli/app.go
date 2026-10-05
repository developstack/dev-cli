// Package cli 是 dev-cli 的命令层：解析参数、编排各子包、把错误翻成给人看的提示。命令分发用标准库 flag
// （设计稿 §10.4：不引入 cobra）。
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/developstack/aidevstack/cli/internal/api"
	"github.com/developstack/aidevstack/cli/internal/buildinfo"
	"github.com/developstack/aidevstack/cli/internal/config"
	"github.com/developstack/aidevstack/cli/internal/credstore"
	"github.com/developstack/aidevstack/cli/internal/launcher"
	"github.com/developstack/aidevstack/cli/internal/paths"
)

// App 是一次 dev-cli 调用的运行环境（测试替换其中的依赖）。
type App struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
	// Interactive 标准输入是终端（歧义时可以交互选择项目）；Run 时按 os.Stdin 判定，测试可直接设置。
	Interactive bool
	// Dirs 用户级目录；零值时按环境解析。
	Dirs paths.Dirs
	// HTTPClient 访问平台用（测试注入 httptest 的 client；nil = 默认）。
	HTTPClient *http.Client
	// Store CLI 凭据存储（nil = 按环境选择钥匙串 / 文件）。
	Store credstore.Store
	// OpenBrowser 打开浏览器（nil = 系统默认）。
	OpenBrowser func(url string) error
	// LoginSleep 登录轮询的等待（测试注入）。
	LoginSleep func(ctx context.Context, d time.Duration) error
	// Now 时钟。
	Now func() time.Time
	// Exec 启动 agent（nil = launcher.Exec：Unix 替换当前进程，Windows 子进程透传退出码）。
	Exec func(binary string, argv, env []string) error
	// Environ 当前环境（nil = os.Environ；测试注入以验证环境黑名单）。
	Environ func() []string
	// Getwd 当前目录（nil = os.Getwd）。
	Getwd func() (string, error)
	// LookPath 在 PATH 里找 agent（nil = exec.LookPath）。
	LookPath func(file string) (string, error)

	debug bool
	// storeWarning 钥匙串不可用时的告警（只打印一次）。
	storeWarning string
}

// command 是一个子命令。
type command struct {
	name    string
	summary string
	run     func(ctx context.Context, a *App, args []string) error
}

// commands 是命令表（help 按这个顺序列出）。
func commands() []command {
	return []command{
		{"login", "sign in to the platform with your browser (device authorization)", runLogin},
		{"logout", "revoke this machine's CLI credential (--all also deletes project credentials)", runLogout},
		{"whoami", "show the signed-in user, platform and credential expiry", runWhoami},
		{"devices", "list your devices, or `devices revoke <id>`", runDevices},
		{"init", "bind this repository to its platform project (writes .aidevstack/, keeps it git-ignored)", runInit},
		{"start", "sync and start an agent: `dev-cli start pi|claude [-- agent args]`", runStart},
		{"sync", "sync credentials and agent config without starting the agent", runSync},
		{"status", "show binding, credential expiry, manifest version and managed-file integrity", runStatus},
		{"doctor", "diagnose git, ignore rules, keychain, platform, agents and leftover provider keys", runDoctor},
		{"version", "print version information", runVersion},
	}
}

// Run 执行一次调用，返回进程退出码。
func (a *App) Run(ctx context.Context, args []string) int {
	a.defaults()
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		a.usage()
		return 0
	}
	if args[0] == "--version" || args[0] == "-v" {
		_, _ = fmt.Fprintln(a.Out, buildinfo.String())
		return 0
	}
	for _, cmd := range commands() {
		if cmd.name != args[0] {
			continue
		}
		err := cmd.run(ctx, a, args[1:])
		if err == nil {
			return 0
		}
		return a.report(err)
	}
	_, _ = fmt.Fprintf(a.Err, "dev-cli: unknown command %q\n\n", args[0])
	a.usage()
	return 2
}

// exitError 让命令指定退出码。
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// report 打印错误并给出退出码。
func (a *App) report(err error) int {
	var exit exitError
	if errors.As(err, &exit) {
		return exit.code
	}
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	var usage usageError
	if errors.As(err, &usage) {
		_, _ = fmt.Fprintf(a.Err, "dev-cli: %s\n", usage.msg)
		return 2
	}
	_, _ = fmt.Fprintf(a.Err, "dev-cli: %s\n", explain(err))
	return 1
}

// usageError 是参数错误（退出码 2）。
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func (a *App) usage() {
	_, _ = fmt.Fprintf(a.Out, "dev-cli %s — start coding agents with your team's platform configuration\n\n",
		buildinfo.CurrentVersion())
	_, _ = fmt.Fprintln(a.Out, "Usage: dev-cli <command> [flags]")
	_, _ = fmt.Fprintln(a.Out)
	for _, cmd := range commands() {
		_, _ = fmt.Fprintf(a.Out, "  %-9s %s\n", cmd.name, cmd.summary)
	}
	_, _ = fmt.Fprintln(a.Out, "\nRun `dev-cli <command> -h` for the flags of a command.")
}

func (a *App) defaults() {
	if a.Out == nil {
		a.Out = os.Stdout
	}
	if a.Err == nil {
		a.Err = os.Stderr
	}
	if a.In == nil {
		a.In = os.Stdin
		if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
			a.Interactive = true
		}
	}
	if a.Exec == nil {
		a.Exec = launcher.Exec
	}
	if a.Environ == nil {
		a.Environ = os.Environ
	}
	if a.Getwd == nil {
		a.Getwd = os.Getwd
	}
	if a.LookPath == nil {
		a.LookPath = exec.LookPath
	}
	if a.Now == nil {
		a.Now = time.Now
	}
}

// dirs 返回用户级目录（零值时按环境解析）。
func (a *App) dirs() (paths.Dirs, error) {
	if a.Dirs.Config != "" {
		return a.Dirs, nil
	}
	dirs, err := paths.Resolve()
	if err != nil {
		return paths.Dirs{}, err
	}
	a.Dirs = dirs
	return dirs, nil
}

// store 返回 CLI 凭据存储（首次使用时选择；钥匙串不可用的告警只打印一次）。
func (a *App) store() (credstore.Store, error) {
	if a.Store != nil {
		return a.Store, nil
	}
	dirs, err := a.dirs()
	if err != nil {
		return nil, err
	}
	a.Store, a.storeWarning = credstore.Open(dirs.CredentialFallbackFile())
	if a.storeWarning != "" {
		_, _ = fmt.Fprintf(a.Err, "warning: %s\n", a.storeWarning)
	}
	return a.Store, nil
}

// platform 决定平台地址（参数 > 环境变量 > 配置文件）。
func (a *App) platform(flagValue string) (string, config.Config, error) {
	dirs, err := a.dirs()
	if err != nil {
		return "", config.Config{}, err
	}
	cfg, err := config.Load(dirs.UserConfigFile())
	if err != nil {
		return "", config.Config{}, err
	}
	platform, err := config.ResolvePlatform(flagValue, cfg)
	return platform, cfg, err
}

// client 创建平台客户端。
func (a *App) client(platform string) *api.Client {
	opts := []api.Option{}
	if a.HTTPClient != nil {
		opts = append(opts, api.WithHTTPClient(a.HTTPClient))
	}
	if a.debug {
		opts = append(opts, api.WithDebug(a.Err))
	}
	return api.New(platform, opts...)
}
