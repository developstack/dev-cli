// Package fakeplatform 是进程内的假平台（httptest），实现 dev-cli 用到的 /api/v1/devenv/* 端点。
//
// 响应体用 internal/api 里**由后端 OpenAPI 生成的 DTO** 组装，形状因此与 spec 一致；后端那一侧由
// backend/test/devenv/transport/schema_contract_test.go 保证真实 handler 满足同一份 spec。
// 行为按后端语义简化：设备码第 N 次轮询后自动批准、开发者密钥 have 有效即 valid、清单 ETag/304。
package fakeplatform

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	goversion "github.com/hashicorp/go-version"

	"github.com/developstack/aidevstack/cli/internal/api"
)

// Platform 是假平台的状态（测试直接读写字段来构造场景）。
type Platform struct {
	Server *httptest.Server

	mu sync.Mutex
	// ApproveAfter 设备码在第几次轮询时批准（之前返回 authorization_pending）。
	ApproveAfter int
	// Deny 为 true 时轮询返回 access_denied。
	Deny bool
	// Polls 已发生的轮询次数。
	Polls int
	// Tokens 有效的 CLI 凭据 → 设备 id。
	Tokens map[string]string
	// Revoked 被 logout / 吊销的设备。
	Revoked []string
	// Remotes 归一化地址 → 项目（resolve 用；不做最长前缀）。
	Remotes map[string]api.DevenvProject
	// Manifest 每个项目的清单。
	Manifest map[string]api.DevenvManifest
	// Bundles 技能包（bundle_url 的路径 → zip 字节）。
	Bundles map[string][]byte
	// Issued 已签发的开发者密钥凭证（id → 到期时间）。
	Issued map[string]time.Time
	// KeyTTL 新签发凭证的有效期。
	KeyTTL time.Duration
	// DeveloperKeyDisabled 为 true 时 ensure 返回 403 developer_key_disabled（管理员停用了开发者密钥）。
	DeveloperKeyDisabled bool
	// MinVersion 非空时，低于它的 dev-cli 返回 426。
	MinVersion string
	// Requests 记录每个请求的 "METHOD path" 与 User-Agent（断言用）。
	Requests []Request
	seq      int
}

// Request 是一次被记录的请求。
type Request struct {
	Target    string
	UserAgent string
	Auth      string
}

// New 启动假平台（测试结束自动关闭）。
func New(t *testing.T) *Platform {
	t.Helper()
	p := &Platform{
		ApproveAfter: 1, Tokens: map[string]string{}, Remotes: map[string]api.DevenvProject{},
		Manifest: map[string]api.DevenvManifest{}, Issued: map[string]time.Time{}, KeyTTL: 7 * 24 * time.Hour,
		Bundles: map[string][]byte{},
	}
	p.Server = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.Server.Close)
	return p
}

// URL 是平台站点根。
func (p *Platform) URL() string { return p.Server.URL }

// Snapshot 在锁内读状态。
func (p *Platform) Snapshot(fn func(p *Platform)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(p)
}

func (p *Platform) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Requests = append(p.Requests, Request{
		Target: r.Method + " " + r.URL.Path, UserAgent: r.UserAgent(), Auth: r.Header.Get("Authorization"),
	})
	if p.MinVersion != "" && below(r.Header.Get("X-Devcli-Version"), p.MinVersion) {
		writeUpgrade(w, p.MinVersion, r.Header.Get("X-Devcli-Version"))
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/devenv/auth/device":
		p.requestDevice(w)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/devenv/auth/token":
		p.pollToken(w)
	default:
		device, ok := p.Tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if !ok {
			writeError(w, http.StatusUnauthorized, "", "unauthorized", nil)
			return
		}
		p.serveAuthed(w, r, device)
	}
}

func (p *Platform) serveAuthed(w http.ResponseWriter, r *http.Request, device string) {
	path := r.URL.Path
	if r.Method == http.MethodGet {
		p.serveRead(w, r, device)
		return
	}
	switch {
	case r.Method == http.MethodDelete && path == "/api/v1/devenv/auth/token":
		p.revoke(device)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/api/v1/devenv/devices/"):
		p.revoke(strings.TrimPrefix(path, "/api/v1/devenv/devices/"))
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && path == "/api/v1/devenv/resolve":
		p.resolve(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/developer-key"):
		p.developerKey(w, r, device)
	default:
		writeError(w, http.StatusNotFound, "not_found", "no such route", nil)
	}
}

// serveRead 处理需鉴权的 GET（设备列表、清单、技能包）。
func (p *Platform) serveRead(w http.ResponseWriter, r *http.Request, device string) {
	path := r.URL.Path
	switch {
	case path == "/api/v1/devenv/devices":
		p.listDevices(w, device)
	case strings.HasSuffix(path, "/manifest"):
		p.manifest(w, r)
	case strings.HasSuffix(path, ".zip"):
		p.bundle(w, path)
	default:
		writeError(w, http.StatusNotFound, "not_found", "no such route", nil)
	}
}

// bundle 返回技能包（没有登记的一律 404，同真实平台）。
func (p *Platform) bundle(w http.ResponseWriter, path string) {
	data, ok := p.Bundles[path]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "skill bundle not found", nil)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	_, _ = w.Write(data)
}

func (p *Platform) requestDevice(w http.ResponseWriter) {
	writeData(w, api.DeviceAuthorization{
		DeviceCode: "device-code", UserCode: "BCDF-GHJK", VerificationURI: p.URL() + "/device",
		VerificationURIComplete: p.URL() + "/device?user_code=BCDF-GHJK", ExpiresIn: 600, Interval: 1,
	})
}

func (p *Platform) pollToken(w http.ResponseWriter) {
	p.Polls++
	if p.Deny {
		writeJSON(w, http.StatusBadRequest, api.OAuthError{Error: api.OAuthErrorErrorAccessDenied})
		return
	}
	if p.Polls < p.ApproveAfter {
		writeJSON(w, http.StatusBadRequest, api.OAuthError{Error: api.OAuthErrorErrorAuthorizationPending})
		return
	}
	p.seq++
	token, device := fmt.Sprintf("adsk_cli_token_%d", p.seq), fmt.Sprintf("cli_dev_%d", p.seq)
	p.Tokens[token] = device
	writeJSON(w, http.StatusOK, api.DeviceTokenResponse{
		AccessToken: token, TokenType: "Bearer", ExpiresIn: 7 * 24 * 3600, Scope: "devenv", DeviceID: device,
		User: struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}{ID: "u-dev", Name: "Dev"},
	})
}

func (p *Platform) revoke(device string) {
	p.Revoked = append(p.Revoked, device)
	for token, d := range p.Tokens {
		if d == device {
			delete(p.Tokens, token)
		}
	}
}

func (p *Platform) listDevices(w http.ResponseWriter, current string) {
	out := []api.Device{}
	for _, device := range p.Tokens {
		out = append(out, api.Device{
			ID: device, DeviceName: "laptop", Os: "linux", Current: device == current,
			CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		})
	}
	writeData(w, out)
}

// writeData 写成功信封。
func writeData(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, map[string]any{"code": 0, "message": "ok", "data": data})
}

// writeError 写失败信封。
func writeError(w http.ResponseWriter, status int, code, message string, details any) {
	body := map[string]any{"code": 10001, "message": message, "data": nil}
	if code != "" {
		body["error_code"] = code
	}
	if details != nil {
		body["details"] = details
	}
	writeJSON(w, status, body)
}

// below 与服务端闸门同一规则：解析不了的版本放行。
func below(current, minimum string) bool {
	cur, err := goversion.NewVersion(current)
	if err != nil {
		return false
	}
	return cur.LessThan(goversion.Must(goversion.NewVersion(minimum)))
}

func writeUpgrade(w http.ResponseWriter, minVersion, current string) {
	details := api.DevenvUpgradeDetails{MinVersion: minVersion, CurrentVersion: current}
	details.Upgrade.Brew = "brew upgrade developstack/tap/dev-cli"
	details.Upgrade.GoInstall = "go install github.com/developstack/aidevstack/cli/cmd/dev-cli@latest"
	details.Upgrade.URL = "https://github.com/developstack/dev-cli/releases"
	writeError(w, http.StatusUpgradeRequired, "cli_upgrade_required", "upgrade", details)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
