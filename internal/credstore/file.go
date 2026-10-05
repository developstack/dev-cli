package credstore

import (
	"path/filepath"
	"sync"

	"github.com/developstack/aidevstack/cli/internal/fsutil"
)

// FileStore 是钥匙串不可用时的回退：用户配置目录下的 credentials.json（0600，目录 0700）。
//
// 安全性弱于钥匙串（同一 OS 用户下的任何进程都能读），doctor 会提示；Windows 上 0600 退化为
// "用户 profile 目录 + 继承 ACL"（设计稿 §10.4）。
type FileStore struct {
	path string
	mu   sync.Mutex
}

// NewFileStore 创建文件存储。
func NewFileStore(path string) *FileStore { return &FileStore{path: path} }

// Kind 实现 Store。
func (*FileStore) Kind() string { return "file" }

// Path 返回文件路径（doctor 展示）。
func (f *FileStore) Path() string { return f.path }

// Load 实现 Store。
func (f *FileStore) Load(platform string) (Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	all, err := f.read()
	if err != nil {
		return Credential{}, err
	}
	r, ok := all[platform]
	if !ok {
		return Credential{}, ErrNotFound
	}
	return fromRecord(platform, r), nil
}

// Save 实现 Store。
func (f *FileStore) Save(cred Credential) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	all, err := f.read()
	if err != nil {
		return err
	}
	all[cred.Platform] = toRecord(cred)
	return f.write(all)
}

// Delete 实现 Store（不存在不报错）。
func (f *FileStore) Delete(platform string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	all, err := f.read()
	if err != nil {
		return err
	}
	if _, ok := all[platform]; !ok {
		return nil
	}
	delete(all, platform)
	return f.write(all)
}

func (f *FileStore) read() (map[string]record, error) {
	all := map[string]record{}
	if _, err := fsutil.ReadJSON(f.path, &all); err != nil {
		return nil, err
	}
	return all, nil
}

func (f *FileStore) write(all map[string]record) error {
	if err := fsutil.EnsureDir(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(f.path, all, 0o600)
}
