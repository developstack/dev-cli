// Package credstore 保存 CLI 凭据（设计稿 §4.5）：优先 OS 钥匙串（macOS Keychain / Linux Secret Service /
// Windows Credential Manager，经 zalando/go-keyring），不可用时回退到用户配置目录下的 0600 文件，并由 doctor 告警。
//
// CLI 凭据**绝不**进项目目录，也不进 agent 进程的环境（它能签发开发者密钥，作用域是该用户的所有项目）。
package credstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/developstack/aidevstack/cli/internal/secret"
)

// ErrNotFound 表示该平台没有保存的 CLI 凭据（需要 login）。
var ErrNotFound = errors.New("credstore: no credential saved for this platform")

// Credential 是一把 CLI 凭据（= 一台设备）。
type Credential struct {
	// Platform 平台站点根（规范化后的 scheme://host[:port]，也是存储键）。
	Platform    string
	AccessToken secret.Secret
	// DeviceID 设备 id（= CLI 凭据 id，"我的设备"里的那一行）。
	DeviceID  string
	UserID    string
	UserName  string
	ExpiresAt time.Time
}

// Expired 报告在 now 时刻是否已过期（留 1 分钟余量，避免请求途中过期）。
func (c Credential) Expired(now time.Time) bool {
	return c.ExpiresAt.IsZero() || !c.ExpiresAt.After(now.Add(time.Minute))
}

// Store 是凭据存储。
type Store interface {
	Load(platform string) (Credential, error)
	Save(cred Credential) error
	Delete(platform string) error
	// Kind 是存储类型（keychain / file），doctor 展示。
	Kind() string
}

// record 是落盘 / 进钥匙串的形状（明文只在这里出现）。
type record struct {
	AccessToken string    `json:"access_token"`
	DeviceID    string    `json:"device_id"`
	UserID      string    `json:"user_id"`
	UserName    string    `json:"user_name"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func toRecord(c Credential) record {
	return record{
		AccessToken: c.AccessToken.Reveal(), DeviceID: c.DeviceID, UserID: c.UserID, UserName: c.UserName,
		ExpiresAt: c.ExpiresAt.UTC(),
	}
}

func fromRecord(platform string, r record) Credential {
	return Credential{
		Platform: platform, AccessToken: secret.New(r.AccessToken), DeviceID: r.DeviceID, UserID: r.UserID,
		UserName: r.UserName, ExpiresAt: r.ExpiresAt,
	}
}

// Open 选择存储：AIDEVSTACK_CREDENTIAL_STORE=file 强制文件；否则探测钥匙串，不可用时回退文件并给出告警文案。
func Open(fallbackFile string) (Store, string) {
	if os.Getenv("AIDEVSTACK_CREDENTIAL_STORE") == "file" {
		return NewFileStore(fallbackFile), ""
	}
	keychain := NewKeychainStore()
	if err := keychain.Probe(); err != nil {
		return NewFileStore(fallbackFile), fmt.Sprintf(
			"OS keychain unavailable (%v); storing the CLI credential in %s (0600)", err, fallbackFile)
	}
	return keychain, ""
}

// marshal / unmarshal 是两种存储共用的编码。
func marshal(c Credential) (string, error) {
	//nolint:gosec // G117：这里就是凭据的落盘 / 入钥匙串编码，明文必须写进去（文件 0600 或 OS 钥匙串）。
	data, err := json.Marshal(toRecord(c))
	if err != nil {
		return "", fmt.Errorf("credstore: encode: %w", err)
	}
	return string(data), nil
}

func unmarshal(platform, data string) (Credential, error) {
	var r record
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		return Credential{}, fmt.Errorf("credstore: decode stored credential: %w", err)
	}
	return fromRecord(platform, r), nil
}
