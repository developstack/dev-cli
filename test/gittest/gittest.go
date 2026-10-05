// Package gittest 在临时目录里建 git 仓库（测试用）。
//
// 子进程环境去掉全部 GIT_*：测试可能在 git 钩子里运行（lefthook pre-push），继承来的 GIT_DIR 会让
// `git init` 打到真实仓库上（scripts/without-git-env.sh 记录过真实事故）。
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Env 返回去掉 GIT_* 的环境，并固定作者信息（CI 里可能没有全局 git 配置）。
func Env() []string {
	env := []string{
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_NOSYSTEM=1",
	}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	return env
}

// Run 在 dir 里执行 git，失败即终止测试。
func Run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	//nolint:gosec,noctx // G204：测试辅助，程序名固定为 git，参数由测试给出。
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Init 建一个带 origin remote 的仓库，返回仓库根（已解析符号链接，macOS 的 /var → /private/var）。
func Init(t *testing.T, origin string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	Run(t, dir, "init", "-q", ".")
	if origin != "" {
		Run(t, dir, "remote", "add", "origin", origin)
	}
	return dir
}
