package repo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/developstack/aidevstack/cli/internal/fsutil"
)

// .gitignore 托管块的标记（重复执行只替换这一块；旧 CLI 的 helper 移植，设计稿 §15.4）。
const (
	blockBegin = "# >>> aidevstack (managed by dev-cli; local credentials, do not commit) >>>"
	blockEnd   = "# <<< aidevstack <<<"
)

// IgnorePattern 是 dev-cli 要求被忽略的路径（整个 .aidevstack/：凭证、项目绑定、锁都是本机状态）。
const IgnorePattern = "/.aidevstack/"

// CheckIgnored 用 `git check-ignore` 判定 relPath 是否被忽略（已被跟踪的文件不算被忽略）。
func CheckIgnored(ctx context.Context, root, relPath string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "-q", "--", filepath.ToSlash(relPath))
	cmd.Env = gitEnv()
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	default:
		return false, fmt.Errorf("git check-ignore %s: %w", relPath, err)
	}
}

// Tracked 报告 relPath 是否已被 git 跟踪（被跟踪的文件不受忽略规则约束，必须先 git rm --cached）。
func Tracked(ctx context.Context, root, relPath string) (bool, error) {
	out, err := git(ctx, root, "ls-files", "--", filepath.ToSlash(relPath))
	return out != "", err
}

// EnsureIgnored 保证 relPath（.aidevstack/ 下的文件）被 git 忽略（设计稿 §4.5 的第 1 / 2 道保险）：
//
//  1. 已被忽略（团队的 .gitignore、全局 excludes 或上次写过）→ 什么都不写；
//  2. 否则写 .git/info/exclude（本机、不产生 diff）与 .gitignore 托管块（随仓库保护其他克隆），再次确认；
//  3. 仍未被忽略（文件已被跟踪，或有否定规则 `!…` 把它放了出来）→ 返回带修复命令的错误，调用方拒绝写凭证。
//
// 返回是否修改了 .gitignore（提示用户提交它）。
func EnsureIgnored(ctx context.Context, root, relPath string) (bool, error) {
	ignored, err := CheckIgnored(ctx, root, relPath)
	if err != nil || ignored {
		return false, err
	}
	if err := appendExclude(ctx, root); err != nil {
		return false, err
	}
	changed, err := writeGitignoreBlock(root)
	if err != nil {
		return false, err
	}
	if ignored, err = CheckIgnored(ctx, root, relPath); err != nil || ignored {
		return changed, err
	}
	if tracked, _ := Tracked(ctx, root, relPath); tracked {
		return changed, fmt.Errorf("%s is tracked by git, so it cannot be ignored; untrack it first:\n"+
			"  git rm --cached %s && git commit -m 'stop tracking dev-cli credentials'\n"+
			"and rotate the credential (dev-cli logout && dev-cli login)", relPath, relPath)
	}
	return changed, fmt.Errorf("%s is still not ignored by git (a negation rule may re-include it); "+
		"inspect with: git check-ignore -v --no-index %s", relPath, relPath)
}

// appendExclude 在 .git/info/exclude 里加一行（worktree 下用 git 告诉我们的真实位置）。
func appendExclude(ctx context.Context, root string) error {
	path, err := git(ctx, root, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(current), "\n") {
		if strings.TrimSpace(line) == IgnorePattern {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	content := strings.TrimRight(string(current), "\n")
	if content != "" {
		content += "\n"
	}
	content += "# dev-cli: local credentials\n" + IgnorePattern + "\n"
	return fsutil.WriteFileAtomic(path, []byte(content), 0o644)
}

// writeGitignoreBlock 写（或替换）.gitignore 托管块；内容未变时不写，返回是否修改了文件。
func writeGitignoreBlock(root string) (bool, error) {
	path := filepath.Join(root, ".gitignore")
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read .gitignore: %w", err)
	}
	block := blockBegin + "\n" + IgnorePattern + "\n" + blockEnd + "\n"
	updated := ReplaceBlock(string(current), block)
	if updated == string(current) {
		return false, nil
	}
	if err := fsutil.WriteFileAtomic(path, []byte(updated), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// ReplaceBlock 用新块替换已有托管块；没有就追加到末尾（与原内容隔一个空行）。
func ReplaceBlock(content, block string) string {
	if begin := strings.Index(content, blockBegin); begin >= 0 {
		if end := strings.Index(content[begin:], blockEnd); end >= 0 {
			tail := strings.TrimPrefix(content[begin+end+len(blockEnd):], "\n")
			return content[:begin] + block + tail
		}
	}
	trimmed := strings.TrimRight(content, "\n")
	if trimmed == "" {
		return block
	}
	return trimmed + "\n\n" + block
}
