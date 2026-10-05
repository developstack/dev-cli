// Package fsutil 是原子写与权限受控的文件操作（托管目录与凭证文件共用）。
package fsutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFileAtomic 先写同目录临时文件、fsync，再 rename 覆盖目标：读者要么看到旧内容、要么看到新内容，
// 进程中途崩溃不会留下半个文件。perm 在创建临时文件时就生效（凭证文件从不以宽权限出现在磁盘上）。
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("fsutil: create temp for %s: %w", path, err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsutil: chmod %s: %w", name, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsutil: write %s: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsutil: sync %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("fsutil: close %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("fsutil: rename %s: %w", path, err)
	}
	return nil
}

// WriteJSONAtomic 以两空格缩进写 JSON（结尾换行），原子替换。
func WriteJSONAtomic(path string, v any, perm fs.FileMode) error {
	data, err := MarshalIndent(v)
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, data, perm)
}

// MarshalIndent 是托管文件统一的 JSON 格式（不转义 HTML 字符，结尾换行；黄金文件依赖它稳定）。
func MarshalIndent(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("fsutil: encode json: %w", err)
	}
	return buf.Bytes(), nil
}

// ReadJSON 读 JSON；文件不存在返回 (false, nil)。
func ReadJSON(path string, v any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("fsutil: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("fsutil: parse %s: %w", path, err)
	}
	return true, nil
}

// EnsureDir 创建目录并收紧权限（已存在的目录也 chmod：防止早先以宽权限创建）。
func EnsureDir(path string, perm fs.FileMode) error {
	if err := os.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("fsutil: mkdir %s: %w", path, err)
	}
	if err := os.Chmod(path, perm); err != nil {
		return fmt.Errorf("fsutil: chmod %s: %w", path, err)
	}
	return nil
}
