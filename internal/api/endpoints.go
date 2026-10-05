package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// RequestDevice 申请设备授权（公开端点）。
func (c *Client) RequestDevice(ctx context.Context, in DeviceAuthorizationRequest) (DeviceAuthorization, error) {
	var out DeviceAuthorization
	err := c.call(ctx, request{method: http.MethodPost, path: "/api/v1/devenv/auth/device", body: in}, &out)
	return out, err
}

// PollToken 轮询一次（RFC 8628 §3.4）：批准后返回 CLI 凭据；未就绪等返回 *PollError。
func (c *Client) PollToken(ctx context.Context, deviceCode string) (DeviceTokenResponse, error) {
	resp, err := c.send(ctx, request{
		method: http.MethodPost, path: "/api/v1/devenv/auth/token",
		body: DeviceTokenRequest{GrantType: "urn:ietf:params:oauth:grant-type:device_code", DeviceCode: deviceCode},
	})
	if err != nil {
		return DeviceTokenResponse{}, err
	}
	status, data := resp.status, resp.body
	switch status {
	case http.StatusOK:
		var out DeviceTokenResponse
		if err := json.Unmarshal(data, &out); err != nil {
			return DeviceTokenResponse{}, fmt.Errorf("api: decode token: %w", err)
		}
		return out, nil
	case http.StatusUpgradeRequired:
		return DeviceTokenResponse{}, decodeError(status, data)
	default:
		var oauth PollError
		var raw struct {
			Error            OAuthErrorError `json:"error"`
			ErrorDescription string          `json:"error_description"`
			Interval         int             `json:"interval"`
		}
		if err := json.Unmarshal(data, &raw); err != nil || raw.Error == "" {
			return DeviceTokenResponse{}, decodeError(status, data)
		}
		oauth = PollError{Code: raw.Error, Description: raw.ErrorDescription, Interval: raw.Interval}
		return DeviceTokenResponse{}, &oauth
	}
}

// Logout 吊销调用者自己这把 CLI 凭据（级联吊销该设备的开发者密钥凭证）。
func (c *Client) Logout(ctx context.Context) error {
	return c.call(ctx, request{method: http.MethodDelete, path: "/api/v1/devenv/auth/token"}, nil)
}

// ListDevices 列出我的设备。
func (c *Client) ListDevices(ctx context.Context) ([]Device, error) {
	var out []Device
	err := c.call(ctx, request{method: http.MethodGet, path: "/api/v1/devenv/devices"}, &out)
	return out, err
}

// RevokeDevice 吊销我的一台设备。
func (c *Client) RevokeDevice(ctx context.Context, deviceID string) error {
	path := "/api/v1/devenv/devices/" + url.PathEscape(deviceID)
	return c.call(ctx, request{method: http.MethodDelete, path: path}, nil)
}

// Resolve 按 remotes 解析项目。
func (c *Client) Resolve(ctx context.Context, in DevenvResolveRequest) (DevenvResolveResult, error) {
	var out DevenvResolveResult
	err := c.call(ctx, request{method: http.MethodPost, path: "/api/v1/devenv/resolve", body: in}, &out)
	return out, err
}

// EnsureDeveloperKey 确保开发者密钥（have 为空或已进入续签窗口时签发新凭证）。
func (c *Client) EnsureDeveloperKey(ctx context.Context, projectID, have string) (DevenvDeveloperKey, error) {
	body := DevenvDeveloperKeyRequest{}
	if have != "" {
		body.HaveCredentialID = &have
	}
	var out DevenvDeveloperKey
	err := c.call(ctx, request{
		method: http.MethodPost, path: "/api/v1/devenv/projects/" + url.PathEscape(projectID) + "/developer-key",
		body: body,
	}, &out)
	return out, err
}

// ManifestResult 是一次清单请求的结果。
type ManifestResult struct {
	Manifest DevenvManifest
	ETag     string
	// NotModified If-None-Match 命中（Manifest 为零值，调用方用本地缓存）。
	NotModified bool
}

// Manifest 拉清单；etag 非空时带 If-None-Match。
func (c *Client) Manifest(ctx context.Context, projectID, etag string) (ManifestResult, error) {
	r := request{method: http.MethodGet, path: "/api/v1/devenv/projects/" + url.PathEscape(projectID) + "/manifest"}
	if etag != "" {
		r.headers = map[string]string{"If-None-Match": etag}
	}
	resp, err := c.send(ctx, r)
	if err != nil {
		return ManifestResult{}, err
	}
	if resp.status == http.StatusNotModified {
		return ManifestResult{ETag: etag, NotModified: true}, nil
	}
	if resp.status != http.StatusOK {
		return ManifestResult{}, decodeError(resp.status, resp.body)
	}
	var env DevenvManifestEnvelope
	if err := json.Unmarshal(resp.body, &env); err != nil {
		return ManifestResult{}, fmt.Errorf("api: decode manifest: %w", err)
	}
	if env.Data == nil {
		return ManifestResult{}, fmt.Errorf("api: manifest response has no data")
	}
	return ManifestResult{Manifest: *env.Data, ETag: resp.header.Get("ETag")}, nil
}

// Healthz 探测平台是否可达（GET /healthz；doctor 用来区分网络 / TLS 问题与鉴权问题）。
func (c *Client) Healthz(ctx context.Context) error {
	resp, err := c.send(ctx, request{method: http.MethodGet, path: "/healthz"})
	if err != nil {
		return err
	}
	if resp.status != http.StatusOK {
		return fmt.Errorf("GET /healthz returned %d", resp.status)
	}
	return nil
}

// maxBundleBytes 是技能包上限（平台单技能内容上限 20 MiB，不压缩的 zip 只多出条目头）。
const maxBundleBytes = 32 << 20

// DownloadSkill 下载技能包（bundleURL 是清单 skills[].bundle_url：相对平台站点根）。
//
// 只接受以 /api/v1/devenv/ 开头的相对地址：清单来自平台，但 CLI 凭据不能被一个被篡改的清单带去别的主机或路径。
// 返回原始字节；digest 由调用方按清单校验（internal/skills）。
func (c *Client) DownloadSkill(ctx context.Context, bundleURL string) ([]byte, error) {
	if !strings.HasPrefix(bundleURL, "/api/v1/devenv/") || strings.Contains(bundleURL, "..") {
		return nil, fmt.Errorf("api: refusing skill bundle url %q (must be a platform-relative devenv path)", bundleURL)
	}
	resp, err := c.send(ctx, request{
		method: http.MethodGet, path: bundleURL, headers: map[string]string{"Accept": "application/zip"},
		maxBytes: maxBundleBytes + 1,
	})
	if err != nil {
		return nil, err
	}
	if resp.status != http.StatusOK {
		return nil, decodeError(resp.status, resp.body)
	}
	if len(resp.body) > maxBundleBytes {
		return nil, fmt.Errorf("api: skill bundle exceeds %d bytes", maxBundleBytes)
	}
	return resp.body, nil
}
