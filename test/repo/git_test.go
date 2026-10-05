package repo_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/developstack/aidevstack/cli/internal/repo"
	"github.com/developstack/aidevstack/cli/test/gittest"
)

// TestLocateAndRemotes 仓库根与子路径；remote 顺序 origin、upstream、其它；指纹只看归一化地址与子路径。
func TestLocateAndRemotes(t *testing.T) {
	ctx := context.Background()
	root := gittest.Init(t, "git@github.com:Acme/Payments.git")
	gittest.Run(t, root, "remote", "add", "zeta", "https://git.example.com/z/z.git")
	gittest.Run(t, root, "remote", "add", "upstream", "https://github.com/acme/payments-upstream")
	sub := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	loc, err := repo.Locate(ctx, sub)
	if err != nil || loc.Root != root || loc.Subpath != "services/api" {
		t.Fatalf("Locate = %+v %v", loc, err)
	}
	remotes, err := repo.Remotes(ctx, root)
	if err != nil || len(remotes) != 3 || remotes[0].Name != "origin" || remotes[1].Name != "upstream" ||
		remotes[2].Name != "zeta" {
		t.Fatalf("Remotes = %+v %v", remotes, err)
	}
	a := repo.Fingerprint(remotes, "services/api")
	remotes[0].URL = "https://github.com/acme/payments"
	if b := repo.Fingerprint(remotes, "services/api"); a != b {
		t.Fatalf("同一仓库换写法不应改变指纹：\n%s\n%s", a, b)
	}
	if c := repo.Fingerprint(remotes, ""); c == a {
		t.Fatal("子路径不同指纹应不同")
	}
	if _, err := repo.Locate(ctx, t.TempDir()); !errors.Is(err, repo.ErrNotRepository) {
		t.Fatalf("不在仓库里 = %v", err)
	}
}

// TestEnsureIgnored 未被忽略时写 .git/info/exclude 与 .gitignore 托管块并确认；重复执行不再改动；
// 已被跟踪的凭证文件拒绝（给出 git rm --cached 修复命令）。
func TestEnsureIgnored(t *testing.T) {
	ctx := context.Background()
	root := gittest.Init(t, "")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const rel = ".aidevstack/credentials.json"
	if ignored, err := repo.CheckIgnored(ctx, root, rel); err != nil || ignored {
		t.Fatalf("初始不应被忽略：%v %v", ignored, err)
	}
	changed, err := repo.EnsureIgnored(ctx, root, rel)
	if err != nil || !changed {
		t.Fatalf("EnsureIgnored = %v %v", changed, err)
	}
	gitignore, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	exclude, _ := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if !strings.HasPrefix(string(gitignore), "node_modules/\n\n# >>> aidevstack") ||
		!strings.Contains(string(gitignore), "/.aidevstack/\n# <<< aidevstack <<<") ||
		!strings.Contains(string(exclude), "/.aidevstack/") {
		t.Fatalf(".gitignore=%q exclude=%q", gitignore, exclude)
	}
	if changed, err := repo.EnsureIgnored(ctx, root, rel); err != nil || changed {
		t.Fatalf("重复执行不应改动：%v %v", changed, err)
	}

	tracked := gittest.Init(t, "")
	if err := os.MkdirAll(filepath.Join(tracked, ".aidevstack"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tracked, rel), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, tracked, "add", "-f", rel)
	_, err = repo.EnsureIgnored(ctx, tracked, rel)
	if err == nil || !strings.Contains(err.Error(), "git rm --cached") {
		t.Fatalf("已被跟踪应拒绝并给出修复命令：%v", err)
	}
}

// TestReplaceBlock 托管块替换而不是追加；没有托管块时追加到末尾。
func TestReplaceBlock(t *testing.T) {
	block := "# >>> aidevstack (managed by dev-cli; local credentials, do not commit) >>>\n/.aidevstack/\n# <<< aidevstack <<<\n"
	once := repo.ReplaceBlock("a\n", block)
	if twice := repo.ReplaceBlock(once, block); twice != once || once != "a\n\n"+block {
		t.Fatalf("once=%q twice=%q", once, twice)
	}
	if repo.ReplaceBlock("", block) != block {
		t.Fatal("空文件")
	}
}
