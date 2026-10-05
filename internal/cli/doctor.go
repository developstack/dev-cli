package cli

// doctor（设计稿 §10.1）：逐项诊断，输出可直接复制的修复命令；有 fail 项时退出码 1。只读，不改任何东西。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/developstack/aidevstack/cli/internal/adapter"
	"github.com/developstack/aidevstack/cli/internal/api"
	"github.com/developstack/aidevstack/cli/internal/credstore"
	"github.com/developstack/aidevstack/cli/internal/fsutil"
	"github.com/developstack/aidevstack/cli/internal/launcher"
	"github.com/developstack/aidevstack/cli/internal/managed"
	"github.com/developstack/aidevstack/cli/internal/repo"
	"github.com/developstack/aidevstack/cli/internal/workspace"
)

// report 收集诊断结果。
type report struct {
	a     *App
	fails int
}

func (r *report) ok(format string, args ...any)   { r.line("ok  ", format, args...) }
func (r *report) warn(format string, args ...any) { r.line("warn", format, args...) }
func (r *report) fail(format string, args ...any) {
	r.fails++
	r.line("FAIL", format, args...)
}

func (r *report) line(level, format string, args ...any) {
	_, _ = fmt.Fprintf(r.a.Out, "[%s] %s\n", level, fmt.Sprintf(format, args...))
}

// runDoctor 处理 `dev-cli doctor`。
func runDoctor(ctx context.Context, a *App, args []string) error {
	var flags commonFlags
	fs := a.newFlagSet("doctor", "doctor [--platform URL]")
	flags.register(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	a.debug = flags.debug
	r := &report{a: a}
	projectID := r.checkRepository(ctx, a)
	r.checkPlatform(ctx, a, flags)
	r.checkEnvironment(a)
	r.checkAgents(ctx, a, projectID)
	if r.fails > 0 {
		return exitError{code: 1}
	}
	return nil
}

// checkRepository 检查 git、仓库、remote、忽略规则与本地凭证；返回绑定的项目 id（没有为空）。
func (r *report) checkRepository(ctx context.Context, a *App) string {
	cwd, err := a.Getwd()
	if err != nil {
		r.fail("cannot read the working directory: %v", err)
		return ""
	}
	loc, err := repo.Locate(ctx, cwd)
	if err != nil {
		r.fail("%v", err)
		return ""
	}
	remotes, _ := repo.Remotes(ctx, loc.Root)
	if len(remotes) == 0 {
		r.fail("repository %s has no git remotes; fix: git remote add origin <url>", loc.Root)
	} else {
		r.ok("repository %s (%d remotes, origin first: %s)", loc.Root, len(remotes), remotes[0].URL)
	}
	rel := workspace.CredentialsRelPath()
	switch ignored, err := repo.CheckIgnored(ctx, loc.Root, rel); {
	case err != nil:
		r.fail("git check-ignore failed: %v", err)
	case ignored:
		r.ok("%s is ignored by git", rel)
	default:
		if tracked, _ := repo.Tracked(ctx, loc.Root, rel); tracked {
			r.fail("%s is TRACKED by git; fix: git rm --cached %s && dev-cli logout && dev-cli login", rel, rel)
		} else {
			r.warn("%s is not ignored yet (dev-cli start adds .aidevstack/ to .gitignore)", rel)
		}
	}
	ws := workspace.Open(loc.Root)
	binding, found, err := ws.LoadProject()
	if err != nil || !found {
		r.warn("repository not bound to a project yet; run: dev-cli init")
		return ""
	}
	r.ok("bound to project %s (%s)", binding.ProjectName, binding.ProjectID)
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(workspace.CredentialsPath(loc.Root)); err == nil && info.Mode().Perm()&0o077 != 0 {
			r.fail("%s is readable by other users (%v); fix: chmod 600 %s", rel, info.Mode().Perm(),
				workspace.CredentialsPath(loc.Root))
		}
	}
	return binding.ProjectID
}

// checkPlatform 检查平台可达、CLI 凭据（存储类型、到期、平台是否还认）与版本兼容。
func (r *report) checkPlatform(ctx context.Context, a *App, flags commonFlags) {
	platform, _, err := a.platform(flags.platform)
	if err != nil {
		r.fail("%v", err)
		return
	}
	client := a.client(platform)
	if err := client.Healthz(ctx); err != nil {
		r.fail("platform %s is unreachable: %v (check the URL, proxy and TLS certificates)", platform, err)
		return
	}
	r.ok("platform %s is reachable", platform)
	store, err := a.store()
	if err != nil {
		r.fail("credential store: %v", err)
		return
	}
	if fileStore, ok := store.(*credstore.FileStore); ok {
		r.warn("CLI credential is stored in a file (%s), not the OS keychain", fileStore.Path())
	} else {
		r.ok("CLI credential store: %s", store.Kind())
	}
	cred, err := store.Load(platform)
	switch {
	case errors.Is(err, credstore.ErrNotFound):
		r.warn("not signed in; run: dev-cli login")
		return
	case err != nil:
		r.fail("read CLI credential: %v", err)
		return
	case cred.Expired(a.Now()):
		r.warn("CLI credential expired; the next start signs in again (or run: dev-cli login)")
		return
	}
	_, err = client.WithToken(cred.AccessToken).ListDevices(ctx)
	var upgrade *api.UpgradeError
	switch {
	case errors.As(err, &upgrade):
		r.fail("%v", upgrade)
	case api.IsUnauthorized(err):
		r.fail("the platform rejects this device's credential (revoked?); run: dev-cli login")
	case err != nil:
		r.fail("platform request failed: %v", err)
	default:
		r.ok("signed in as %s; credential valid until %s", displayUser(cred),
			cred.ExpiresAt.Local().Format("2006-01-02"))
	}
}

// checkEnvironment 报告环境里会被 start 去掉的厂商凭据（只列名字）。
func (r *report) checkEnvironment(a *App) {
	var names []string
	for _, kv := range a.Environ() {
		if name, _, ok := strings.Cut(kv, "="); ok && launcher.Blocked(name) {
			names = append(names, name)
		}
	}
	if len(names) > 0 {
		r.warn("provider credentials in your environment are removed when dev-cli starts an agent: %s",
			strings.Join(names, ", "))
	} else {
		r.ok("no provider credentials in the environment")
	}
}

// checkAgents 检查各 agent 的安装与版本、托管目录的完整性与残留的自带凭据。
func (r *report) checkAgents(ctx context.Context, a *App, projectID string) {
	dirs, err := a.dirs()
	if err != nil {
		r.fail("%v", err)
		return
	}
	var cache manifestCache
	cached, _ := fsutil.ReadJSON(dirs.ManifestCache(projectID), &cache)
	for _, name := range []string{"pi", "claude"} {
		agent := adapters()[name]
		_, installed, err := a.checkAgent(ctx, agent)
		switch {
		case err != nil:
			r.warn("%s: not installed; install with: %s", name, agent.UpgradeHint())
		case cached && adapter.VersionBelow(installed, agent.MinVersion(cache.Manifest)):
			r.fail("%s %s is older than the platform requires (%s); upgrade with: %s", name, installed,
				agent.MinVersion(cache.Manifest), agent.UpgradeHint())
		default:
			r.ok("%s %s", name, installed)
		}
		if projectID == "" {
			continue
		}
		dir := dirs.AgentDir(projectID, name)
		for _, forbidden := range agent.Forbidden() {
			if present, _ := managed.ForbiddenPresent(dir, forbidden); present {
				r.warn("%s: %s exists in the managed directory (bring-your-own login); start removes it",
					name, forbidden)
			}
		}
		tampered, err := managed.Verify(dir, managedStatePath(dirs.State, projectID, name))
		if err == nil && len(tampered) > 0 {
			r.warn("%s: managed files modified outside dev-cli: %s (restored on the next start)", name,
				strings.Join(tampered, ", "))
		}
	}
}
