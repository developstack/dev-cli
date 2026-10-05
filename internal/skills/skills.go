// Package skills 把清单里的技能装进 agent 的托管目录（设计稿 §6.3，ADR-0028）。
//
// 每个技能装在 <托管目录>/skills/<name>/（pi 用 `--skill` 逐个显式加载，Claude Code 的 CLAUDE_CONFIG_DIR/skills
// 是 personal 级技能目录）。流程：按 digest 取包（本地缓存命中就不下载）→ sha256 必须等于清单里的 digest，否则拒绝
// 写入任何东西 → 解压到同级临时目录（zip-slip 防护、条目数与单条大小上限）→ 整体替换旧目录。
// skills/ 下只保留清单列出的技能：吊销或项目停用后，下一次同步删除本地副本；托管目录不允许混入别的技能。
package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/developstack/aidevstack/cli/internal/api"
	"github.com/developstack/aidevstack/cli/internal/fsutil"
)

// 解压上限（沿用旧 dev-cli 的 extract 规则；服务端单技能合计上限 20 MiB，远在其内）。
const (
	maxEntries    = 2000
	maxEntryBytes = 16 << 20
	digestPrefix  = "sha256:"
	stateFile     = ".installed.json"
)

// ErrDigestMismatch 表示下载的技能包与清单里的 digest 不一致（被篡改或传输出错）：拒绝安装。
var ErrDigestMismatch = errors.New("skills: bundle digest does not match the manifest")

// Downloader 下载技能包（bundleURL 相对平台站点根，带 CLI 凭据）。
type Downloader interface {
	DownloadSkill(ctx context.Context, bundleURL string) ([]byte, error)
}

// Result 是一次同步做了什么。
type Result struct {
	Installed []string
	Removed   []string
}

// installed 是 skills/ 下已装技能的记录（名称 → digest），用来跳过未变化的技能。
type installed struct {
	Skills map[string]string `json:"skills"`
}

// Sync 让 skillsDir 恰好包含 want 里的技能（cacheDir 按 digest 缓存技能包）。
func Sync(ctx context.Context, dl Downloader, cacheDir, skillsDir string, want []api.DevenvSkill) (Result, error) {
	var result Result
	if err := fsutil.EnsureDir(skillsDir, 0o700); err != nil {
		return result, err
	}
	state := installed{Skills: map[string]string{}}
	if _, err := fsutil.ReadJSON(filepath.Join(skillsDir, stateFile), &state); err != nil {
		return result, err
	}
	if state.Skills == nil {
		state.Skills = map[string]string{}
	}
	names := make([]string, 0, len(want))
	for i := range want {
		skill := want[i]
		if err := validName(skill.Name); err != nil {
			return result, err
		}
		names = append(names, skill.Name)
		dir := filepath.Join(skillsDir, skill.Name)
		if state.Skills[skill.Name] == skill.Digest && isDir(dir) {
			continue
		}
		data, err := fetch(ctx, dl, cacheDir, skill)
		if err != nil {
			return result, err
		}
		if err := install(data, skill.Name, skillsDir); err != nil {
			return result, fmt.Errorf("skills: install %s %s: %w", skill.Name, skill.Version, err)
		}
		state.Skills[skill.Name] = skill.Digest
		result.Installed = append(result.Installed, skill.Name)
	}
	removed, err := prune(skillsDir, names)
	if err != nil {
		return result, err
	}
	result.Removed = removed
	for name := range state.Skills {
		if !slices.Contains(names, name) {
			delete(state.Skills, name)
		}
	}
	return result, fsutil.WriteJSONAtomic(filepath.Join(skillsDir, stateFile), state, 0o600)
}

// fetch 取技能包：缓存命中（且校验通过）直接用，否则下载、校验后写缓存。校验失败不写任何文件。
func fetch(ctx context.Context, dl Downloader, cacheDir string, skill api.DevenvSkill) ([]byte, error) {
	sum, ok := strings.CutPrefix(skill.Digest, digestPrefix)
	if !ok || len(sum) != sha256.Size*2 {
		return nil, fmt.Errorf("skills: %s has an invalid digest %q", skill.Name, skill.Digest)
	}
	cached := filepath.Join(cacheDir, sum+".zip")
	if data, err := os.ReadFile(cached); err == nil && digestOf(data) == sum {
		return data, nil
	}
	data, err := dl.DownloadSkill(ctx, skill.BundleURL)
	if err != nil {
		return nil, fmt.Errorf("skills: download %s %s: %w", skill.Name, skill.Version, err)
	}
	if digestOf(data) != sum {
		return nil, fmt.Errorf("%w: %s %s", ErrDigestMismatch, skill.Name, skill.Version)
	}
	if err := fsutil.EnsureDir(cacheDir, 0o700); err != nil {
		return nil, err
	}
	if err := fsutil.WriteFileAtomic(cached, data, 0o600); err != nil {
		return nil, err
	}
	return data, nil
}

// install 解压到同级临时目录，再整体替换 skillsDir/<name>（旧目录先挪开再删：任何时刻目标要么是旧版、要么是新版）。
func install(data []byte, name, skillsDir string) error {
	tmp, err := os.MkdirTemp(skillsDir, ".tmp-"+name+"-")
	if err != nil {
		return fmt.Errorf("skills: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := extract(data, name, tmp); err != nil {
		return err
	}
	target := filepath.Join(skillsDir, name)
	old := ""
	if isDir(target) {
		old = filepath.Join(skillsDir, ".old-"+name+"-"+randomSuffix())
		if err := os.Rename(target, old); err != nil {
			return fmt.Errorf("skills: move old %s: %w", name, err)
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		if old != "" {
			_ = os.Rename(old, target)
		}
		return fmt.Errorf("skills: activate %s: %w", name, err)
	}
	if old != "" {
		_ = os.RemoveAll(old)
	}
	return nil
}

// extract 解压技能包：只接受 `<name>/` 下的普通文件，剥掉外层目录；拒绝越界路径、符号链接与超限条目。
func extract(data []byte, name, dst string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("skills: open bundle: %w", err)
	}
	if len(r.File) > maxEntries {
		return fmt.Errorf("skills: bundle has %d entries (limit %d)", len(r.File), maxEntries)
	}
	for _, f := range r.File {
		rel, ok := strings.CutPrefix(f.Name, name+"/")
		if !ok || rel == "" {
			return fmt.Errorf("skills: entry %q is outside %s/", f.Name, name)
		}
		if strings.HasSuffix(rel, "/") {
			continue
		}
		if !f.Mode().IsRegular() {
			return fmt.Errorf("skills: entry %q is not a regular file", f.Name)
		}
		clean := path.Clean(rel)
		if clean != rel || strings.HasPrefix(clean, "../") || path.IsAbs(clean) || strings.Contains(clean, "\\") {
			return fmt.Errorf("skills: entry %q escapes the skill directory", f.Name)
		}
		if f.UncompressedSize64 > maxEntryBytes {
			return fmt.Errorf("skills: entry %q exceeds %d bytes", f.Name, maxEntryBytes)
		}
		if err := writeEntry(f, filepath.Join(dst, filepath.FromSlash(clean))); err != nil {
			return err
		}
	}
	return nil
}

// writeEntry 写一个条目（读取时再卡一次上限：不信任 zip 头里的大小）。
func writeEntry(f *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return fmt.Errorf("skills: mkdir for %s: %w", f.Name, err)
	}
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("skills: open %s: %w", f.Name, err)
	}
	defer func() { _ = rc.Close() }()
	content, err := io.ReadAll(io.LimitReader(rc, maxEntryBytes+1))
	if err != nil {
		return fmt.Errorf("skills: read %s: %w", f.Name, err)
	}
	if len(content) > maxEntryBytes {
		return fmt.Errorf("skills: entry %q exceeds %d bytes", f.Name, maxEntryBytes)
	}
	//nolint:gosec // G306：技能文件是给 agent 读的说明与脚本，0644 与包里的权限一致。
	if err := os.WriteFile(target, content, 0o644); err != nil {
		return fmt.Errorf("skills: write %s: %w", f.Name, err)
	}
	return nil
}

// prune 删除 skills/ 下不在清单里的技能目录（以及上次中断留下的临时目录），返回删掉的技能名。
func prune(skillsDir string, keep []string) ([]string, error) {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil, fmt.Errorf("skills: list %s: %w", skillsDir, err)
	}
	var removed []string
	for _, e := range entries {
		name := e.Name()
		if name == stateFile || slices.Contains(keep, name) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(skillsDir, name)); err != nil {
			return removed, fmt.Errorf("skills: remove %s: %w", name, err)
		}
		if !strings.HasPrefix(name, ".") {
			removed = append(removed, name)
		}
	}
	return removed, nil
}

// validName 校验技能名（平台 slug 规则：小写字母、数字与单个连字符）—— 它会成为本地目录名。
func validName(name string) error {
	if name == "" || len(name) > 64 || strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") ||
		strings.Contains(name, "--") {
		return fmt.Errorf("skills: invalid skill name %q", name)
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return fmt.Errorf("skills: invalid skill name %q", name)
		}
	}
	return nil
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func randomSuffix() string {
	var buf [6]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
