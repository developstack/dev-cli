package repo_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/developstack/aidevstack/cli/internal/repo"
)

// vectors 是 contracts/devenv/repo-normalize.json（后端与 dev-cli 共用的唯一权威）。
type vectors struct {
	URLs []struct {
		Input      string  `json:"input"`
		Normalized *string `json:"normalized"`
	} `json:"urls"`
	PathPrefixes []struct {
		Input      string  `json:"input"`
		Normalized *string `json:"normalized"`
	} `json:"path_prefixes"`
}

// vectorsPath 自测试文件所在目录逐级向上找 contracts/devenv/repo-normalize.json：
// 主仓里它在仓库根，公开镜像仓（developstack/dev-cli，cli/ 为根）里发布流程把它放在镜像根。
func vectorsPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	rel := filepath.Join("contracts", "devenv", "repo-normalize.json")
	for dir := filepath.Dir(thisFile); ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			return filepath.Join(dir, rel)
		}
		if filepath.Dir(dir) == dir {
			t.Fatalf("shared vectors %s not found above %s", rel, filepath.Dir(thisFile))
		}
	}
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	raw, err := os.ReadFile(vectorsPath(t))
	if err != nil {
		t.Fatalf("read shared vectors: %v", err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.URLs) == 0 || len(v.PathPrefixes) == 0 {
		t.Fatal("共享向量为空")
	}
	return v
}

// TestSharedNormalizationVectors dev-cli 的归一化与后端读同一份向量（分叉即失败）。
func TestSharedNormalizationVectors(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.URLs {
		got, err := repo.NormalizeRepoURL(c.Input)
		switch {
		case c.Normalized == nil && err == nil:
			t.Errorf("NormalizeRepoURL(%q) = %q, want rejection", c.Input, got)
		case c.Normalized != nil && (err != nil || got != *c.Normalized):
			t.Errorf("NormalizeRepoURL(%q) = %q, %v; want %q", c.Input, got, err, *c.Normalized)
		}
	}
	for _, c := range v.PathPrefixes {
		got, err := repo.NormalizePathPrefix(c.Input)
		switch {
		case c.Normalized == nil && err == nil:
			t.Errorf("NormalizePathPrefix(%q) = %q, want rejection", c.Input, got)
		case c.Normalized != nil && (err != nil || got != *c.Normalized):
			t.Errorf("NormalizePathPrefix(%q) = %q, %v; want %q", c.Input, got, err, *c.Normalized)
		}
	}
}
