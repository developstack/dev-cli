package project

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/developstack/dev-cli/internal/agents"
	"github.com/developstack/dev-cli/internal/config"
)

// 托管块的标记。用标记包起来的好处：**重复执行只替换这一块**，不会越写越多。
//
// 学习点：往别人的 .gitignore 里追加内容要有边界 —— 否则跑三次 init 就留三份重复条目，
// 而且用户手写的规则会被搅在一起。
const (
	beginMarker = "# >>> dev-cli（同步产物不入库，请勿手改本块）>>>"
	endMarker   = "# <<< dev-cli <<<"
)

// IgnorePatterns 返回需要 git 忽略的路径。
//
// 学习点（为什么是这些）：**同步产物**和**凭据**都不该进仓库 ——
//  1. settings.json 里有 API Key；
//  2. skills/ 是平台下发的副本，入库会造成"同一份内容两处维护"，
//     还会把第三方技能的内容混进你的提交历史。
//
// 而 `.dev-cli/rules|design|docs|MCP` 是**团队自己写**的资产，应当入库，所以不在这里。
func IgnorePatterns() []string {
	patterns := []string{
		config.DirName + "/settings.json",
		config.DirName + "/skills/",
	}
	for _, dir := range agents.SkillDirs() {
		patterns = append(patterns, dir+"/")
	}
	return patterns
}

// EnsureGitignore 写入托管的忽略块（幂等）。
func EnsureGitignore(projectRoot string) error {
	path := filepath.Join(projectRoot, ".gitignore")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("dev-cli: 读取 .gitignore 失败: %w", err)
	}

	block := beginMarker + "\n" + strings.Join(IgnorePatterns(), "\n") + "\n" + endMarker + "\n"
	updated := replaceBlock(string(existing), block)
	if updated == string(existing) {
		return nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("dev-cli: 写入 .gitignore 失败: %w", err)
	}
	return nil
}

// replaceBlock 用新块替换已有块；没有就追加。
func replaceBlock(content, block string) string {
	begin := strings.Index(content, beginMarker)
	if begin >= 0 {
		if end := strings.Index(content[begin:], endMarker); end >= 0 {
			tail := content[begin+end+len(endMarker):]
			tail = strings.TrimPrefix(tail, "\n")
			return content[:begin] + block + tail
		}
	}
	trimmed := strings.TrimRight(content, "\n")
	if trimmed == "" {
		return block
	}
	return trimmed + "\n\n" + block
}

// TrackedPaths 返回其中**已被 git 跟踪**的路径（不在 git 仓库里则返回空）。
func TrackedPaths(projectRoot string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		cmd := exec.Command("git", "-C", projectRoot, "ls-files", "--", p)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			continue
		}
		if strings.TrimSpace(stdout.String()) != "" {
			out = append(out, p)
		}
	}
	return out
}

// Untrack 把已跟踪的路径从索引里移除，**保留工作区文件**。
//
// 学习点：`git rm --cached` 而不是 `git rm` —— 技能文件必须留在磁盘上（agent 要用），
// 只是不该进版本库。用 `--ignore-unmatch` 保证路径不存在时不报错。
func Untrack(projectRoot string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"-C", projectRoot, "rm", "-r", "--cached", "--ignore-unmatch", "--quiet", "--"}, paths...)
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("dev-cli: 解除跟踪失败: %w（%s）", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// InGitRepo 判断项目是否在 git 仓库里（不在就跳过所有 git 相关处理）。
func InGitRepo(projectRoot string) bool {
	cmd := exec.Command("git", "-C", projectRoot, "rev-parse", "--is-inside-work-tree")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	return cmd.Run() == nil && strings.TrimSpace(stdout.String()) == "true"
}
