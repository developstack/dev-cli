package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Error 是平台返回的失败（统一信封：error_code 是稳定错误码，客户端只按它分支，不读 message）。
type Error struct {
	Status    int
	Code      int
	ErrorCode DevenvErrorCode
	Message   string
	Details   json.RawMessage
	RequestID string
}

// Error 实现 error。
func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	if e.ErrorCode != "" {
		msg = fmt.Sprintf("%s (%s)", msg, e.ErrorCode)
	}
	if e.RequestID != "" {
		msg += " [request " + e.RequestID + "]"
	}
	return msg
}

// IsCode 报告 err 是否是带该错误码的平台错误。
func IsCode(err error, code DevenvErrorCode) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.ErrorCode == code
}

// IsUnauthorized 报告是否是 401（CLI 凭据过期 / 被吊销 / 不存在）。
func IsUnauthorized(err error) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized
}

// UpgradeError 是 426：这个版本的 dev-cli 已不受支持。
type UpgradeError struct {
	Details DevenvUpgradeDetails
}

// Error 实现 error（附可直接复制的升级命令）。
func (e *UpgradeError) Error() string {
	d := e.Details
	return fmt.Sprintf("this dev-cli (%s) is no longer supported by the platform; version %s or later is required.\n"+
		"  upgrade: %s\n       or: %s\n     docs: %s", d.CurrentVersion, d.MinVersion, d.Upgrade.Brew,
		d.Upgrade.GoInstall, d.Upgrade.URL)
}

// PollError 是设备授权 token 端点的 RFC 8628 错误（authorization_pending / slow_down / …）。
type PollError struct {
	Code        OAuthErrorError
	Description string
	// Interval slow_down 时服务端要求的新间隔（秒）。
	Interval int
}

// Error 实现 error。
func (e *PollError) Error() string {
	if e.Description != "" {
		return string(e.Code) + ": " + e.Description
	}
	return string(e.Code)
}

// errorEnvelope 是失败信封的公共字段（各端点的 Envelope 类型都有这些字段）。
type errorEnvelope struct {
	Code      int             `json:"code"`
	Message   string          `json:"message"`
	ErrorCode DevenvErrorCode `json:"error_code"`
	Details   json.RawMessage `json:"details"`
	RequestID string          `json:"request_id"`
}

// decodeError 把非 2xx 响应翻成 *Error / *UpgradeError。
func decodeError(status int, body []byte) error {
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		return &Error{Status: status, Message: snippet}
	}
	if status == http.StatusUpgradeRequired || env.ErrorCode == DevenvErrorCodeCliUpgradeRequired {
		var details DevenvUpgradeDetails
		_ = json.Unmarshal(env.Details, &details)
		return &UpgradeError{Details: details}
	}
	return &Error{
		Status: status, Code: env.Code, ErrorCode: env.ErrorCode, Message: env.Message, Details: env.Details,
		RequestID: env.RequestID,
	}
}

// ResolveDetails 是 resolve 失败的 details（未登记 / 歧义 / 不是成员）。
type ResolveDetails struct {
	Candidates []struct {
		ProjectID     string `json:"project_id"`
		ProjectName   string `json:"project_name"`
		TeamName      string `json:"team_name"`
		Manager       string `json:"manager"`
		URLNormalized string `json:"url_normalized"`
		PathPrefix    string `json:"path_prefix"`
		Remote        string `json:"remote"`
	} `json:"candidates"`
	NormalizedURLs []string `json:"normalized_urls"`
}

// ResolveDetailsOf 解析 resolve 失败的 details（不是 resolve 错误时返回零值）。
func ResolveDetailsOf(err error) ResolveDetails {
	var apiErr *Error
	var details ResolveDetails
	if errors.As(err, &apiErr) && len(apiErr.Details) > 0 {
		_ = json.Unmarshal(apiErr.Details, &details)
	}
	return details
}
