// Package auth 是 dev-cli 的设备授权登录（RFC 8628 形状，设计稿 §4.3）：申请设备码 → 自动打开浏览器到
// verification_uri_complete（预填 user_code）→ 用户在控制台核对后批准 → 轮询拿到 CLI 凭据。
package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/developstack/aidevstack/cli/internal/api"
	"github.com/developstack/aidevstack/cli/internal/buildinfo"
	"github.com/developstack/aidevstack/cli/internal/credstore"
	"github.com/developstack/aidevstack/cli/internal/secret"
)

// 轮询节流（RFC 8628 §3.5：slow_down 把间隔加 5 秒）。
const (
	slowDownStep   = 5 * time.Second
	minPollEvery   = time.Second
	defaultPollGap = 5 * time.Second
)

// ErrDenied 表示用户在浏览器里拒绝了这次登录。
var ErrDenied = errors.New("login was denied in the browser")

// ErrExpired 表示设备码过期（10 分钟内没有批准）。
var ErrExpired = errors.New("the login code expired before it was approved; run the command again")

// Options 是登录参数。
type Options struct {
	// Out 给用户看的提示（user_code、URL）。
	Out io.Writer
	// NoBrowser 只打印 URL 与代码（SSH 场景）。
	NoBrowser bool
	// OpenBrowser 打开浏览器（测试注入；nil = OpenURL）。
	OpenBrowser func(url string) error
	// Sleep 等待（测试注入；nil = 可取消的 time.Sleep）。
	Sleep func(ctx context.Context, d time.Duration) error
	// Now 时钟（测试注入；nil = time.Now）。
	Now func() time.Time
}

// Login 走一遍设备授权，返回 CLI 凭据（调用方负责保存）。
func Login(ctx context.Context, client *api.Client, opts Options) (credstore.Credential, error) {
	opts = withDefaults(opts)
	hostname, _ := os.Hostname()
	grant, err := client.RequestDevice(ctx, api.DeviceAuthorizationRequest{
		ClientID: "dev-cli", DeviceName: &hostname, Os: ptr(runtime.GOOS), Arch: ptr(runtime.GOARCH),
		CliVersion: ptr(buildinfo.CurrentVersion()),
	})
	if err != nil {
		return credstore.Credential{}, err
	}
	announce(opts, grant)
	token, err := poll(ctx, client, grant, opts)
	if err != nil {
		return credstore.Credential{}, err
	}
	return credstore.Credential{
		Platform: client.Base(), AccessToken: secret.New(token.AccessToken), DeviceID: token.DeviceID,
		UserID: token.User.ID, UserName: token.User.Name,
		ExpiresAt: opts.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second),
	}, nil
}

// announce 打印 user_code 并尝试打开浏览器（打不开就只打印 —— 用户可以手动打开或在另一台设备上打开）。
func announce(opts Options, grant api.DeviceAuthorization) {
	_, _ = fmt.Fprintf(opts.Out, "To sign in, confirm this code in your browser:\n\n    %s\n\n", grant.UserCode)
	if opts.NoBrowser || Headless() {
		_, _ = fmt.Fprintf(opts.Out, "Open %s on any device and enter the code.\n", grant.VerificationURI)
		return
	}
	if err := opts.OpenBrowser(grant.VerificationURIComplete); err != nil {
		_, _ = fmt.Fprintf(opts.Out, "Could not open a browser (%v).\nOpen %s and enter the code.\n",
			err, grant.VerificationURI)
		return
	}
	_, _ = fmt.Fprintf(opts.Out, "Opened %s\nWaiting for approval…\n", grant.VerificationURI)
}

// poll 按 interval 轮询直到批准、拒绝或过期。
func poll(ctx context.Context, client *api.Client, grant api.DeviceAuthorization, opts Options) (
	api.DeviceTokenResponse, error,
) {
	interval := time.Duration(grant.Interval) * time.Second
	if interval < minPollEvery {
		interval = defaultPollGap
	}
	deadline := opts.Now().Add(time.Duration(grant.ExpiresIn) * time.Second)
	for {
		if err := opts.Sleep(ctx, interval); err != nil {
			return api.DeviceTokenResponse{}, err
		}
		token, err := client.PollToken(ctx, grant.DeviceCode)
		if err == nil {
			return token, nil
		}
		var pollErr *api.PollError
		if !errors.As(err, &pollErr) {
			return api.DeviceTokenResponse{}, err
		}
		switch pollErr.Code { //nolint:exhaustive // 其余错误码（invalid_grant 等）一律按失败处理，见 default
		case api.OAuthErrorErrorAuthorizationPending:
		case api.OAuthErrorErrorSlowDown:
			interval += slowDownStep
			if pollErr.Interval > 0 {
				interval = time.Duration(pollErr.Interval) * time.Second
			}
		case api.OAuthErrorErrorAccessDenied:
			return api.DeviceTokenResponse{}, ErrDenied
		case api.OAuthErrorErrorExpiredToken:
			return api.DeviceTokenResponse{}, ErrExpired
		default:
			return api.DeviceTokenResponse{}, fmt.Errorf("login failed: %w", pollErr)
		}
		if opts.Now().After(deadline) {
			return api.DeviceTokenResponse{}, ErrExpired
		}
	}
}

func withDefaults(opts Options) Options {
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	if opts.OpenBrowser == nil {
		opts.OpenBrowser = OpenURL
	}
	if opts.Sleep == nil {
		opts.Sleep = sleepContext
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return opts
}

// sleepContext 是可被 Ctrl-C 打断的等待。
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func ptr[T any](v T) *T { return &v }
