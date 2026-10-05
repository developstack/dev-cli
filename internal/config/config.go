// Package config 是 dev-cli 的用户偏好（config.json，无秘密）。
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/developstack/aidevstack/cli/internal/fsutil"
)

// Config 是用户偏好。
type Config struct {
	// Platform 平台站点根（如 https://ai.example.com）；login --platform 写入。
	Platform string `json:"platform,omitempty"`
	// DefaultAgent start 不带 agent 时用哪个（pi 优先）。
	DefaultAgent string `json:"default_agent,omitempty"`
}

// Load 读配置；文件不存在返回零值。
func Load(path string) (Config, error) {
	var cfg Config
	if _, err := fsutil.ReadJSON(path, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save 原子写配置（0600：虽然不含秘密，平台地址也属于内部信息）。
func Save(path string, cfg Config) error {
	if err := fsutil.EnsureDir(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(path, cfg, 0o600)
}

// ErrNoPlatform 表示还没有配置平台地址。
var ErrNoPlatform = errors.New("no platform configured: run `dev-cli login --platform https://<your-platform>`")

// ResolvePlatform 决定平台地址：参数 > AIDEVSTACK_PLATFORM > 配置文件；并规范化。
func ResolvePlatform(flag string, cfg Config) (string, error) {
	raw := strings.TrimSpace(flag)
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("AIDEVSTACK_PLATFORM"))
	}
	if raw == "" {
		raw = cfg.Platform
	}
	if raw == "" {
		return "", ErrNoPlatform
	}
	return NormalizePlatform(raw)
}

// NormalizePlatform 校验并规范化平台地址：只要 scheme + host[:port]（去掉路径、query、末尾斜杠）。
//
// 非 https 只允许回环地址（设计稿 T9：明文 HTTP 会把 CLI 凭据与开发者密钥暴露给网络上的任何人）；
// 内网只有 HTTP 的部署须显式设置 AIDEVSTACK_ALLOW_INSECURE_HTTP=1 承担这个风险。
func NormalizePlatform(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid platform URL %q", raw)
	}
	scheme := strings.ToLower(u.Scheme)
	switch {
	case scheme == "https":
	case scheme == "http" && (isLoopback(u.Hostname()) || os.Getenv("AIDEVSTACK_ALLOW_INSECURE_HTTP") == "1"):
	case scheme == "http":
		return "", fmt.Errorf("refusing plain http platform %q: use https, or set AIDEVSTACK_ALLOW_INSECURE_HTTP=1 "+
			"if this internal deployment has no TLS (credentials would travel unencrypted)", raw)
	default:
		return "", fmt.Errorf("unsupported platform URL scheme %q", u.Scheme)
	}
	return scheme + "://" + strings.ToLower(u.Host), nil
}

// isLoopback 报告 host 是否是本机回环。
func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
