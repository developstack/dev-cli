// Package api 是 dev-cli 对平台 /api/v1/devenv/* 的 HTTP 客户端。DTO 由后端 OpenAPI 生成（dto.gen.go）。
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/developstack/aidevstack/cli/internal/buildinfo"
	"github.com/developstack/aidevstack/cli/internal/secret"
)

// maxResponseBytes 是响应体上限（清单是最大的响应，正常远小于此；防止异常响应把内存打满）。
const maxResponseBytes = 8 << 20

// defaultTimeout 是单次请求的总时限。
const defaultTimeout = 30 * time.Second

// Client 是平台客户端。零值不可用，用 New 创建。
type Client struct {
	base  string
	hc    *http.Client
	token secret.Secret
	// debug 非空时记录每个请求的方法、路径、状态与耗时（**不**记录任何头与请求 / 响应体）。
	debug io.Writer
}

// Option 配置客户端。
type Option func(*Client)

// WithHTTPClient 替换底层 http.Client（测试用）。
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// WithDebug 打开请求日志（--debug）。
func WithDebug(w io.Writer) Option { return func(c *Client) { c.debug = w } }

// New 创建客户端；base 是平台站点根（config.NormalizePlatform 的结果）。
func New(base string, opts ...Option) *Client {
	c := &Client{base: strings.TrimRight(base, "/"), hc: &http.Client{Timeout: defaultTimeout}}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WithToken 返回带 CLI 凭据的副本。
func (c *Client) WithToken(token secret.Secret) *Client {
	cp := *c
	cp.token = token
	return &cp
}

// Base 返回平台站点根。
func (c *Client) Base() string { return c.base }

// request 是一次调用。
type request struct {
	method  string
	path    string
	body    any
	headers map[string]string
	// maxBytes 响应体上限（0 = maxResponseBytes；技能包更大）。
	maxBytes int64
}

// response 是一次请求的结果（响应体有上限）。
type response struct {
	status int
	header http.Header
	body   []byte
}

// send 发请求并返回状态、头与（有上限的）响应体。网络错误原样返回。
func (c *Client) send(ctx context.Context, r request) (response, error) {
	var body io.Reader
	if r.body != nil {
		data, err := json.Marshal(r.body)
		if err != nil {
			return response{}, fmt.Errorf("api: encode request: %w", err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, c.base+r.path, body)
	if err != nil {
		return response{}, fmt.Errorf("api: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	req.Header.Set("X-Devcli-Version", buildinfo.CurrentVersion())
	if !c.token.Empty() {
		req.Header.Set("Authorization", "Bearer "+c.token.Reveal())
	}
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := c.hc.Do(req)
	if err != nil {
		return response{}, fmt.Errorf("api: %s %s: %w", r.method, r.path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	limit := r.maxBytes
	if limit <= 0 {
		limit = maxResponseBytes
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if c.debug != nil {
		_, _ = fmt.Fprintf(c.debug, "debug: %s %s -> %d (%s)\n", r.method, r.path, resp.StatusCode,
			time.Since(start).Round(time.Millisecond))
	}
	if err != nil {
		return response{}, fmt.Errorf("api: read %s %s: %w", r.method, r.path, err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: data}, nil
}

// call 发请求，2xx 时把信封的 data 解到 out（out 为 nil 时忽略响应体），否则返回平台错误。
func (c *Client) call(ctx context.Context, r request, out any) error {
	resp, err := c.send(ctx, r)
	if err != nil {
		return err
	}
	status, data := resp.status, resp.body
	if status < 200 || status >= 300 {
		return decodeError(status, data)
	}
	if out == nil || status == http.StatusNoContent {
		return nil
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("api: decode %s %s: %w", r.method, r.path, err)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("api: decode %s %s data: %w", r.method, r.path, err)
	}
	return nil
}
