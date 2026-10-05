package cli

// 账号类命令：login / logout / whoami / devices / version。

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/developstack/aidevstack/cli/internal/api"
	"github.com/developstack/aidevstack/cli/internal/buildinfo"
	"github.com/developstack/aidevstack/cli/internal/config"
	"github.com/developstack/aidevstack/cli/internal/credstore"
	"github.com/developstack/aidevstack/cli/internal/workspace"
)

// newFlagSet 创建子命令的 flag set（错误输出到 a.Err，-h 返回 flag.ErrHelp）。
func (a *App) newFlagSet(name, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.Err)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(a.Err, "Usage: dev-cli %s\n\nFlags:\n", usage)
		fs.PrintDefaults()
	}
	return fs
}

// parseFlags 解析参数；参数错误翻成 usageError（退出码 2），-h 原样返回 flag.ErrHelp（退出码 0）。
func parseFlags(fs *flag.FlagSet, args []string) error {
	err := fs.Parse(args)
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return err
	}
	return usageError{msg: err.Error()}
}

// runLogin 处理 `dev-cli login [--platform URL] [--no-browser]`：总是重新授权（换一把新的 CLI 凭据）。
func runLogin(ctx context.Context, a *App, args []string) error {
	var flags commonFlags
	fs := a.newFlagSet("login", "login [--platform URL] [--no-browser]")
	flags.register(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	a.debug = flags.debug
	platform, cfg, err := a.platform(flags.platform)
	if err != nil {
		return err
	}
	if flags.platform != "" && cfg.Platform != platform {
		cfg.Platform = platform
		dirs, _ := a.dirs()
		if err := config.Save(dirs.UserConfigFile(), cfg); err != nil {
			return err
		}
	}
	store, err := a.store()
	if err != nil {
		return err
	}
	previous, prevErr := store.Load(platform)
	s, err := a.relogin(ctx, a.client(platform), store, flags)
	if err != nil {
		return err
	}
	// 换下来的旧凭据顺手吊销（否则"我的设备"里会留下一台同名的孤儿设备直到 7 天后过期）；失败不影响登录。
	if prevErr == nil && previous.DeviceID != s.cred.DeviceID && !previous.Expired(a.Now()) {
		_ = a.client(platform).WithToken(previous.AccessToken).Logout(ctx)
	}
	return nil
}

// runLogout 处理 `dev-cli logout [--all]`：吊销本机 CLI 凭据（服务端级联吊销该设备的开发者密钥凭证）。
func runLogout(ctx context.Context, a *App, args []string) error {
	var flags commonFlags
	var all bool
	fs := a.newFlagSet("logout", "logout [--all] [--platform URL]")
	flags.register(fs)
	fs.BoolVar(&all, "all", false, "also delete .aidevstack/credentials.json in every repository bound on this machine")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	a.debug = flags.debug
	platform, _, err := a.platform(flags.platform)
	if err != nil {
		return err
	}
	store, err := a.store()
	if err != nil {
		return err
	}
	cred, err := store.Load(platform)
	switch {
	case errors.Is(err, credstore.ErrNotFound):
		_, _ = fmt.Fprintf(a.Out, "Not signed in to %s.\n", platform)
	case err != nil:
		return err
	default:
		if err := a.client(platform).WithToken(cred.AccessToken).Logout(ctx); err != nil && !api.IsUnauthorized(err) {
			return fmt.Errorf("revoke the credential on the platform: %w (local credential kept; retry later)", err)
		}
		if err := store.Delete(platform); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(a.Out, "Signed out of %s; this device's credential is revoked.\n", platform)
	}
	if !all {
		return nil
	}
	dirs, err := a.dirs()
	if err != nil {
		return err
	}
	removed, err := workspace.NewIndex(dirs.WorkspaceIndex()).RemoveAllCredentials()
	_, _ = fmt.Fprintf(a.Out, "Deleted developer key credentials in %d repositories.\n", removed)
	return err
}

// runWhoami 处理 `dev-cli whoami`：本地凭据信息 + 向平台确认它仍然有效。
func runWhoami(ctx context.Context, a *App, args []string) error {
	var flags commonFlags
	fs := a.newFlagSet("whoami", "whoami [--platform URL]")
	flags.register(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	a.debug = flags.debug
	platform, _, err := a.platform(flags.platform)
	if err != nil {
		return err
	}
	store, err := a.store()
	if err != nil {
		return err
	}
	cred, err := store.Load(platform)
	if errors.Is(err, credstore.ErrNotFound) {
		return fmt.Errorf("not signed in to %s: run `dev-cli login`", platform)
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(a.Out, "user:      %s (%s)\nplatform:  %s\ndevice:    %s\nexpires:   %s\nstored in: %s\n",
		displayUser(cred), cred.UserID, platform, cred.DeviceID, cred.ExpiresAt.Local().Format(time.DateTime),
		store.Kind())
	if cred.Expired(a.Now()) {
		return errors.New("the credential has expired: run `dev-cli login`")
	}
	if _, err := a.client(platform).WithToken(cred.AccessToken).ListDevices(ctx); err != nil {
		if api.IsUnauthorized(err) {
			return errors.New("the platform no longer accepts this credential (revoked?): run `dev-cli login`")
		}
		return err
	}
	_, _ = fmt.Fprintln(a.Out, "status:    valid")
	return nil
}

// runDevices 处理 `dev-cli devices` 与 `dev-cli devices revoke <id>`。
func runDevices(ctx context.Context, a *App, args []string) error {
	var flags commonFlags
	fs := a.newFlagSet("devices", "devices [revoke <device-id>] [--platform URL]")
	flags.register(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	rest := fs.Args()
	s, err := a.signedIn(ctx, flags)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		if rest[0] != "revoke" || len(rest) != 2 {
			return usageError{"usage: dev-cli devices revoke <device-id>"}
		}
		revoke := func(s *session) error { return s.client.RevokeDevice(ctx, rest[1]) }
		if err := a.withRelogin(ctx, s, flags, revoke); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(a.Out, "Revoked device %s and its developer key credentials.\n", rest[1])
		return nil
	}
	var devices []api.Device
	if err := a.withRelogin(ctx, s, flags, func(s *session) error {
		devices, err = s.client.ListDevices(ctx)
		return err
	}); err != nil {
		return err
	}
	w := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tNAME\tOS\tCREATED\tEXPIRES\tLAST USED\t")
	for _, d := range devices {
		mark := ""
		if d.Current {
			mark = "(this device)"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", d.ID, d.DeviceName, d.Os,
			d.CreatedAt.Local().Format(time.DateOnly), timeOrDash(d.ExpiresAt), timeOrDash(d.LastUsedAt), mark)
	}
	return w.Flush()
}

func timeOrDash(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Local().Format(time.DateTime)
}

// runVersion 处理 `dev-cli version`。
func runVersion(_ context.Context, a *App, _ []string) error {
	_, _ = fmt.Fprintln(a.Out, buildinfo.String())
	return nil
}
