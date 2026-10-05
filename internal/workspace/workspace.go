package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"github.com/developstack/aidevstack/cli/internal/fsutil"
	"github.com/developstack/aidevstack/cli/internal/secret"
)

// lockTimeout 是等待仓库锁的上限（同一仓库里并发的 dev-cli 串行化"同步与写入"，设计稿 §6.3）。
const lockTimeout = 30 * time.Second

// Workspace 是一个仓库里的 `.aidevstack/`。
type Workspace struct {
	Root string
}

// Open 返回仓库的工作区（不创建目录）。
func Open(root string) Workspace { return Workspace{Root: root} }

// EnsureDir 创建 `.aidevstack/`（0700）。
func (w Workspace) EnsureDir() error { return fsutil.EnsureDir(Dir(w.Root), 0o700) }

// Project 是仓库与项目的绑定（project.json，无秘密）。
type Project struct {
	Platform    string    `json:"platform"`
	ProjectID   string    `json:"project_id"`
	ProjectName string    `json:"project_name"`
	TeamName    string    `json:"team_name,omitempty"`
	Remote      string    `json:"matched_remote"`
	URL         string    `json:"matched_url"`
	PathPrefix  string    `json:"path_prefix"`
	Fingerprint string    `json:"remotes_fingerprint"`
	BoundAt     time.Time `json:"bound_at"`
}

// LoadProject 读绑定；没有绑定返回 (零值, false, nil)。
func (w Workspace) LoadProject() (Project, bool, error) {
	var p Project
	found, err := fsutil.ReadJSON(filepath.Join(Dir(w.Root), "project.json"), &p)
	return p, found, err
}

// SaveProject 写绑定。
func (w Workspace) SaveProject(p Project) error {
	if err := w.EnsureDir(); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(filepath.Join(Dir(w.Root), "project.json"), p, 0o600)
}

// Credentials 是开发者密钥在本仓库的凭证（credentials.json，0600，必须被 git 忽略；R5"VK 沉淀到本地"）。
type Credentials struct {
	Platform     string
	ProjectID    string
	VirtualKeyID string
	CredentialID string
	Prefix       string
	Secret       secret.Secret
	ExpiresAt    time.Time
	// RenewBefore 续签窗口（服务端在 ensure 响应里给出）。
	RenewBefore time.Duration
}

// credentialsFile 是落盘形状（明文只在这里出现）。
type credentialsFile struct {
	Platform           string    `json:"platform"`
	ProjectID          string    `json:"project_id"`
	VirtualKeyID       string    `json:"virtual_key_id"`
	CredentialID       string    `json:"credential_id"`
	Prefix             string    `json:"prefix"`
	Secret             string    `json:"secret"`
	ExpiresAt          time.Time `json:"expires_at"`
	RenewBeforeSeconds int64     `json:"renew_before_seconds"`
}

// LoadCredentials 读凭证；没有返回 (零值, false, nil)。
func (w Workspace) LoadCredentials() (Credentials, bool, error) {
	var f credentialsFile
	found, err := fsutil.ReadJSON(CredentialsPath(w.Root), &f)
	if err != nil || !found {
		return Credentials{}, found, err
	}
	return Credentials{
		Platform: f.Platform, ProjectID: f.ProjectID, VirtualKeyID: f.VirtualKeyID, CredentialID: f.CredentialID,
		Prefix: f.Prefix, Secret: secret.New(f.Secret), ExpiresAt: f.ExpiresAt,
		RenewBefore: time.Duration(f.RenewBeforeSeconds) * time.Second,
	}, true, nil
}

// SaveCredentials 原子写凭证（0600；调用方必须先确认 .aidevstack/ 已被 git 忽略）。
func (w Workspace) SaveCredentials(c Credentials) error {
	if err := w.EnsureDir(); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(CredentialsPath(w.Root), credentialsFile{
		Platform: c.Platform, ProjectID: c.ProjectID, VirtualKeyID: c.VirtualKeyID, CredentialID: c.CredentialID,
		Prefix: c.Prefix, Secret: c.Secret.Reveal(), ExpiresAt: c.ExpiresAt.UTC(),
		RenewBeforeSeconds: int64(c.RenewBefore / time.Second),
	}, 0o600)
}

// NeedsRenewal 报告本地凭证是否需要（静默）续签：属于别的平台 / 项目、已过期，或剩余有效期不超过续签窗口。
func (c Credentials) NeedsRenewal(platform, projectID string, now time.Time) bool {
	if c.Secret.Empty() || c.Platform != platform || c.ProjectID != projectID {
		return true
	}
	window := c.RenewBefore
	if window <= 0 {
		window = 48 * time.Hour
	}
	return !c.ExpiresAt.After(now.Add(window))
}

// Lock 是仓库级进程锁（.aidevstack/.lock；flock / LockFileEx，进程退出时由 OS 释放，没有"过期锁"问题）。
type Lock struct {
	fl *flock.Flock
}

// ErrLocked 表示另一个 dev-cli 进程长时间持有本仓库的锁。
var ErrLocked = errors.New("another dev-cli is syncing this repository; try again in a moment")

// Lock 取锁（最多等 30 秒）。
func (w Workspace) Lock(ctx context.Context) (*Lock, error) {
	if err := w.EnsureDir(); err != nil {
		return nil, err
	}
	fl := flock.New(filepath.Join(Dir(w.Root), ".lock"))
	ctx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	ok, err := fl.TryLockContext(ctx, 200*time.Millisecond)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("lock %s: %w", Dir(w.Root), err)
	}
	if !ok {
		return nil, ErrLocked
	}
	return &Lock{fl: fl}, nil
}

// Unlock 释放锁（可重复调用）。
func (l *Lock) Unlock() {
	if l != nil && l.fl != nil {
		_ = l.fl.Unlock()
	}
}

// CredentialsRelPath 是凭证文件相对仓库根的路径（git check-ignore 用）。
func CredentialsRelPath() string { return filepath.ToSlash(filepath.Join(DirName, "credentials.json")) }

// Exists 报告 path 是否存在。
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
