// Package platform 是 dev-cli 与 aidevstack 平台之间的 HTTP 客户端。
//
// 学习点：CLI 只依赖**契约形状**（平台侧 `/api/local-agent/*` 与 `/api/projects/mine`），
// 不依赖平台内部实现 —— 所以这里手写三个端点的请求/响应结构，而不是引入 SDK。
package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 默认参数。
const (
	// DefaultEndpoint 默认平台地址（本地开发）。
	DefaultEndpoint = "http://localhost:8080"
	// requestTimeout 单次管理请求超时。
	requestTimeout = 30 * time.Second
	// downloadTimeout 技能包下载超时（包可能几百 KB）。
	downloadTimeout = 60 * time.Second
	// maxDownloadBytes 单包大小上限（防止把内存吃满）。
	maxDownloadBytes = 16 << 20
)

// ErrUnauthorized 表示密钥无效或已被吊销。
var ErrUnauthorized = errors.New("dev-cli: API Key 无效或已被吊销")

// Project 是平台上的一个项目（绑定用）。
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Command 是平台下发的一条待执行指令。
type Command struct {
	ID           int64  `json:"id"`
	Type         string `json:"type"`
	SkillSlug    string `json:"skill_slug,omitempty"`
	SkillVersion string `json:"skill_version,omitempty"`
	DownloadURL  string `json:"download_url,omitempty"`
	// ModelConfig 是模型网关配置（apply_model_config 指令携带）。
	ModelConfig *ModelConfig `json:"model_config,omitempty"`
}

// ModelConfig 是平台下发的模型网关配置（**虚拟密钥**，只对平台 /v1 有效）。
//
// 学习点：上游供应商的真钥永远留在服务端；下发的是可限预算、可吊销的虚拟密钥 ——
// 所以即使它落到开发机，爆炸半径也远小于供应商真钥。
type ModelConfig struct {
	BaseURL          string   `json:"base_url"`
	VirtualKey       string   `json:"virtual_key,omitempty"`
	VirtualKeySecret string   `json:"virtual_key_secret,omitempty"`
	Provider         string   `json:"provider,omitempty"`
	Models           []string `json:"models,omitempty"`
}

// 命令类型。
const (
	// CommandInstallSkill 安装技能。
	CommandInstallSkill = "install_skill"
	// CommandUninstallSkill 卸载技能。
	CommandUninstallSkill = "uninstall_skill"
	// CommandApplyModelConfig 应用模型网关配置。
	CommandApplyModelConfig = "apply_model_config"
)

// InstalledSkill 是客户端上报的"已装技能"。
type InstalledSkill struct {
	Slug    string `json:"slug"`
	Version string `json:"version,omitempty"`
}

// Client 是平台 API 客户端。
type Client struct {
	endpoint string
	apiKey   string
	http     *http.Client
}

// New 创建客户端（endpoint 为空时用默认值）。
func New(endpoint, apiKey string) *Client {
	trimmed := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if trimmed == "" {
		trimmed = DefaultEndpoint
	}
	return &Client{
		endpoint: trimmed,
		apiKey:   strings.TrimSpace(apiKey),
		http:     &http.Client{Timeout: requestTimeout},
	}
}

// Endpoint 返回平台根地址。
func (c *Client) Endpoint() string { return c.endpoint }

// Projects 拉取"当前密钥可见的项目"（绑定时的选择列表）。
//
// 学习点：先拉列表再让用户选 —— 而不是让用户手抄项目 id。平台侧按调用者的组范围收窄，
// 所以这个列表本身就是**授权结果**。
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var out struct {
		Projects []Project `json:"projects"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/projects/mine", nil, &out); err != nil {
		return nil, err
	}
	return out.Projects, nil
}

// SyncRequest 是拉取待执行命令的请求体。
type SyncRequest struct {
	AgentID         string           `json:"local_agent_id"`
	AgentType       string           `json:"agent_type"`
	ProjectID       string           `json:"project_id"`
	Scope           string           `json:"scope"`
	InstalledSkills []InstalledSkill `json:"installed_skills"`
}

// SyncResponse 是平台的响应。
type SyncResponse struct {
	OK       bool      `json:"ok"`
	SyncID   string    `json:"sync_id,omitempty"`
	Commands []Command `json:"commands"`
}

// Sync 拉取待执行命令（安装/卸载差异）。
func (c *Client) Sync(ctx context.Context, req SyncRequest) (SyncResponse, error) {
	var out SyncResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/local-agent/sync", req, &out); err != nil {
		return SyncResponse{}, err
	}
	return out, nil
}

// Ack 回报一条命令的执行结果。
func (c *Client) Ack(ctx context.Context, id int64, status, errMsg string) error {
	body := map[string]any{"id": id, "status": status}
	if errMsg != "" {
		body["error"] = errMsg
	}
	return c.doJSON(ctx, http.MethodPost, "/api/local-agent/commands/ack", body, nil)
}

// DownloadPackage 下载技能分发包（zip）。
//
// 学习点：这个端点**故意不鉴权**（客户端下载是裸请求），所以这里不带任何头。
func (c *Client) DownloadPackage(ctx context.Context, url string) ([]byte, error) {
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("dev-cli: 下载地址为空")
	}
	client := &http.Client{Timeout: downloadTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("dev-cli: 构造下载请求失败: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dev-cli: 下载技能包失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dev-cli: 下载技能包失败: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes))
	if err != nil {
		return nil, fmt.Errorf("dev-cli: 读取技能包失败: %w", err)
	}
	return body, nil
}

// doJSON 发一次 JSON 请求（鉴权用 Bearer；401/403 归一成 ErrUnauthorized）。
func (c *Client) doJSON(ctx context.Context, method, path string, payload, out any) error {
	var reader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("dev-cli: 序列化请求失败: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, reader)
	if err != nil {
		return fmt.Errorf("dev-cli: 构造请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("X-API-Token", c.apiKey)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("dev-cli: 请求 %s 失败（平台地址 %s）: %w", path, c.endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrUnauthorized
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("dev-cli: 读取响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("dev-cli: 请求 %s 失败: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("dev-cli: 解析响应失败: %w", err)
	}
	return nil
}
