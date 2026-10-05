// Package managed 维护托管配置目录（PI_CODING_AGENT_DIR / CLAUDE_CONFIG_DIR，设计稿 §6.3）。
//
// 托管目录里 dev-cli 写的文件由它全权拥有：每次同步整体重渲染（原子写），记录每个文件的 sha256；
// 下次同步前比对，发现被用户或 agent 改过就覆盖并告警（威胁 T5：一致性护栏，不是 DRM）。
// 上次写过、这次不再需要的文件删除；"禁止存在"的文件（pi 的 auth.json：在托管目录里 /login 产生的自带凭据）
// 每次都删，但只有带内容的才报告 —— pi 1.0 每次启动都会建一个空的 auth.json（`{}`），那不是自带凭据，
// 照报的话每次 start 都是一条误报。agent 自己写的文件（会话、缓存）不在记录里，不动。
package managed

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/developstack/aidevstack/cli/internal/fsutil"
)

// File 是托管目录里的一个文件。
type File struct {
	// Path 相对托管目录的路径（正斜杠）。
	Path string
	Data []byte
	Mode fs.FileMode
}

// Result 是一次同步做了什么（给人看的告警由调用方打印）。
type Result struct {
	// Tampered 上次写过、此后被改动（这次已覆盖）的文件。
	Tampered []string
	// Removed 删除的禁止文件与不再需要的旧文件。
	Removed []string
	// Forbidden 本次删除的、带内容的"禁止存在"的文件（如存了凭据的 auth.json）；空占位文件静默删除、不计入。
	Forbidden []string
}

// state 是记录（相对路径 → sha256）。
type state struct {
	Files map[string]string `json:"files"`
}

// Sync 把 files 写进 dir，记录写到 statePath；forbidden 里的文件存在即删除。
func Sync(dir, statePath string, files []File, forbidden []string) (Result, error) {
	var result Result
	if err := fsutil.EnsureDir(dir, 0o700); err != nil {
		return result, err
	}
	prev, err := loadState(statePath)
	if err != nil {
		return result, err
	}
	if result.Tampered, err = Tampered(dir, prev.Files); err != nil {
		return result, err
	}
	if result.Forbidden, err = removeForbidden(dir, forbidden); err != nil {
		return result, err
	}
	next, err := writeAll(dir, files)
	if err != nil {
		return result, err
	}
	var stale []string
	for name := range prev.Files {
		if _, keep := next.Files[name]; !keep {
			stale = append(stale, name)
		}
	}
	slices.Sort(stale)
	if result.Removed, err = removeAll(dir, stale); err != nil {
		return result, err
	}
	if err := fsutil.EnsureDir(filepath.Dir(statePath), 0o700); err != nil {
		return result, err
	}
	return result, fsutil.WriteJSONAtomic(statePath, next, 0o600)
}

// writeAll 原子写全部文件，返回新的记录。
func writeAll(dir string, files []File) (state, error) {
	next := state{Files: make(map[string]string, len(files))}
	for _, f := range files {
		path, err := within(dir, f.Path)
		if err != nil {
			return state{}, err
		}
		if err := fsutil.EnsureDir(filepath.Dir(path), 0o700); err != nil {
			return state{}, err
		}
		if err := fsutil.WriteFileAtomic(path, f.Data, f.Mode); err != nil {
			return state{}, err
		}
		next.Files[f.Path] = digest(f.Data)
	}
	return next, nil
}

// removeForbidden 删除存在的禁止文件，返回其中带内容的那些（空占位文件也删，但不报告）。
func removeForbidden(dir string, names []string) ([]string, error) {
	var reported []string
	for _, name := range names {
		present, err := ForbiddenPresent(dir, name)
		if err != nil {
			return reported, err
		}
		if _, err := removeIfExists(dir, name); err != nil {
			return reported, err
		}
		if present {
			reported = append(reported, name)
		}
	}
	return reported, nil
}

// ForbiddenPresent 报告托管目录里的禁止文件是否存在且带内容（空文件、`{}`、`[]`、`null` 视为 agent 建的占位，不算）。
func ForbiddenPresent(dir, name string) (bool, error) {
	path, err := within(dir, name)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("managed: read %s: %w", path, err)
	}
	switch strings.TrimSpace(string(data)) {
	case "", "{}", "[]", "null":
		return false, nil
	}
	return true, nil
}

// removeAll 删除存在的文件，返回实际删除的那些。
func removeAll(dir string, names []string) ([]string, error) {
	var removed []string
	for _, name := range names {
		ok, err := removeIfExists(dir, name)
		if err != nil {
			return removed, err
		}
		if ok {
			removed = append(removed, name)
		}
	}
	return removed, nil
}

// Verify 返回自上次同步以来被改动或删除的托管文件（status / doctor 用）。
func Verify(dir, statePath string) ([]string, error) {
	prev, err := loadState(statePath)
	if err != nil {
		return nil, err
	}
	return Tampered(dir, prev.Files)
}

// Tampered 比对记录与磁盘：内容变了或文件没了的都算。
func Tampered(dir string, recorded map[string]string) ([]string, error) {
	var out []string
	for name, sum := range recorded {
		path, err := within(dir, name)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && digest(data) != sum) {
			out = append(out, name)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("managed: read %s: %w", path, err)
		}
	}
	slices.Sort(out)
	return out, nil
}

func loadState(path string) (state, error) {
	s := state{Files: map[string]string{}}
	if _, err := fsutil.ReadJSON(path, &s); err != nil {
		return state{}, err
	}
	if s.Files == nil {
		s.Files = map[string]string{}
	}
	return s, nil
}

func removeIfExists(dir, name string) (bool, error) {
	path, err := within(dir, name)
	if err != nil {
		return false, err
	}
	err = os.Remove(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("managed: remove %s: %w", path, err)
	}
}

// within 把相对路径解析到 dir 之下，拒绝逃逸（`..`、绝对路径）。
func within(dir, rel string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || clean == "." {
		return "", fmt.Errorf("managed: invalid path %q", rel)
	}
	path := filepath.Join(dir, clean)
	back, err := filepath.Rel(dir, path)
	if err != nil || back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("managed: path %q escapes the managed directory", rel)
	}
	return path, nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
