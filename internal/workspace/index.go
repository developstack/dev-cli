// Package workspace 管理仓库工作树里 dev-cli 唯一会写的东西：`.aidevstack/`（设计稿 §4.5）。
package workspace

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/developstack/aidevstack/cli/internal/fsutil"
)

// Index 是本机绑定过的仓库根列表（用户级状态目录里的 workspaces.json）。
//
// 只用于 `logout --all`：删除各仓库 `.aidevstack/credentials.json` 里的开发者密钥凭证。
type Index struct {
	path string
}

// NewIndex 打开索引文件。
func NewIndex(path string) Index { return Index{path: path} }

type indexFile struct {
	Roots []string `json:"roots"`
}

// Add 记下一个仓库根（幂等）。
func (i Index) Add(root string) error {
	var f indexFile
	if _, err := fsutil.ReadJSON(i.path, &f); err != nil {
		return err
	}
	if slices.Contains(f.Roots, root) {
		return nil
	}
	f.Roots = append(f.Roots, root)
	slices.Sort(f.Roots)
	if err := fsutil.EnsureDir(filepath.Dir(i.path), 0o700); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(i.path, f, 0o600)
}

// Roots 返回记下的仓库根。
func (i Index) Roots() ([]string, error) {
	var f indexFile
	if _, err := fsutil.ReadJSON(i.path, &f); err != nil {
		return nil, err
	}
	return f.Roots, nil
}

// RemoveAllCredentials 删除所有记下的仓库里的开发者密钥凭证文件，返回删除的数量（仓库已不存在的跳过）。
func (i Index) RemoveAllCredentials() (int, error) {
	roots, err := i.Roots()
	if err != nil {
		return 0, err
	}
	removed := 0
	var errs []error
	for _, root := range roots {
		err := os.Remove(CredentialsPath(root))
		switch {
		case err == nil:
			removed++
		case errors.Is(err, fs.ErrNotExist):
		default:
			errs = append(errs, err)
		}
	}
	return removed, errors.Join(errs...)
}

// Dir 是仓库里的 `.aidevstack/` 目录。
func Dir(root string) string { return filepath.Join(root, DirName) }

// DirName 是仓库内目录名。
const DirName = ".aidevstack"

// CredentialsPath 是开发者密钥凭证文件（0600，必须被 git 忽略）。
func CredentialsPath(root string) string { return filepath.Join(Dir(root), "credentials.json") }
