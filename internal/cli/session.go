package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/developstack/aidevstack/cli/internal/api"
	"github.com/developstack/aidevstack/cli/internal/auth"
	"github.com/developstack/aidevstack/cli/internal/credstore"
)

// commonFlags 是多数命令共用的参数。
type commonFlags struct {
	platform  string
	debug     bool
	noBrowser bool
}

// register 把共用参数挂到 flag set 上。
func (c *commonFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.platform, "platform", "", "platform URL (default: AIDEVSTACK_PLATFORM or the saved platform)")
	fs.BoolVar(&c.debug, "debug", false, "log each platform request (method, path, status; never headers or bodies)")
	fs.BoolVar(&c.noBrowser, "no-browser", false, "do not open a browser; print the sign-in URL and code instead")
}

// session 是已登录的平台会话。
type session struct {
	client *api.Client
	cred   credstore.Credential
	store  credstore.Store
}

// signedIn 返回已登录的会话；凭据缺失或已过期时自动走浏览器登录（用户拍板 Q5：CLI 凭据 7 天到期才开浏览器）。
func (a *App) signedIn(ctx context.Context, flags commonFlags) (*session, error) {
	a.debug = a.debug || flags.debug
	platform, _, err := a.platform(flags.platform)
	if err != nil {
		return nil, err
	}
	store, err := a.store()
	if err != nil {
		return nil, err
	}
	base := a.client(platform)
	cred, err := store.Load(platform)
	switch {
	case errors.Is(err, credstore.ErrNotFound):
		_, _ = fmt.Fprintf(a.Err, "Not signed in to %s.\n", platform)
		return a.relogin(ctx, base, store, flags)
	case err != nil:
		return nil, err
	case cred.Expired(a.Now()):
		_, _ = fmt.Fprintln(a.Err, "Your dev-cli sign-in expired (credentials last 7 days).")
		return a.relogin(ctx, base, store, flags)
	}
	return &session{client: base.WithToken(cred.AccessToken), cred: cred, store: store}, nil
}

// relogin 走浏览器登录并保存凭据。
func (a *App) relogin(
	ctx context.Context, base *api.Client, store credstore.Store, flags commonFlags,
) (*session, error) {
	cred, err := auth.Login(ctx, base, auth.Options{
		Out: a.Err, NoBrowser: flags.noBrowser, OpenBrowser: a.OpenBrowser, Sleep: a.LoginSleep, Now: a.Now,
	})
	if err != nil {
		return nil, err
	}
	if err := store.Save(cred); err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(a.Err, "Signed in as %s (this device's credential expires %s).\n",
		displayUser(cred), cred.ExpiresAt.Local().Format(time.DateTime))
	return &session{client: base.WithToken(cred.AccessToken), cred: cred, store: store}, nil
}

// withRelogin 执行 fn；遇到 401（CLI 凭据被吊销 / 服务端判定过期）时重新登录一次再执行（设计稿 §10.2）。
func (a *App) withRelogin(ctx context.Context, s *session, flags commonFlags, fn func(*session) error) error {
	err := fn(s)
	if !api.IsUnauthorized(err) {
		return err
	}
	_, _ = fmt.Fprintln(a.Err, "The platform rejected this device's credential (revoked or expired); signing in again.")
	_ = s.store.Delete(s.cred.Platform)
	fresh, err := a.relogin(ctx, a.client(s.cred.Platform), s.store, flags)
	if err != nil {
		return err
	}
	*s = *fresh
	return fn(s)
}

// displayUser 是给人看的用户名。
func displayUser(cred credstore.Credential) string {
	if cred.UserName != "" {
		return cred.UserName
	}
	return cred.UserID
}
