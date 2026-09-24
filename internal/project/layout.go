// Package project 负责项目侧的目录布局：`.dev-cli/` 骨架、AGENTS.md/CLAUDE.md 检查、
// 以及"同步产物不入库"的 git 处理。
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/developstack/dev-cli/internal/config"
)

// SubDirs 是 `.dev-cli/` 下的子目录（约定的资产分类）。
//
// 学习点：目录先占好位 —— 团队知道"东西该放哪"，平台后续也能按分类分发
// （现在是 skills，之后 MCP/rules/design/docs 各走各的）。
func SubDirs() []string {
	return []string{"skills", "MCP", "rules", "design", "docs"}
}

// MemoryFiles 是各工具读的"项目说明"文件（检查它们是否存在）。
func MemoryFiles() []string {
	return []string{"AGENTS.md", "CLAUDE.md"}
}

// EnsureLayout 创建 `.dev-cli/` 骨架（幂等）。
func EnsureLayout(projectRoot string) error {
	for _, sub := range SubDirs() {
		dir := filepath.Join(projectRoot, config.DirName, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("dev-cli: 创建 %s 失败: %w", dir, err)
		}
	}
	return nil
}

// CheckMemoryFiles 检查 AGENTS.md / CLAUDE.md 是否存在，返回状态描述。
//
// 学习点：**只检查、不创建** —— 这两个文件是项目的门面文档，自动生成一份空壳
// 只会让人以为"已经有了"。缺失就如实报出来，让用户自己写。
func CheckMemoryFiles(projectRoot string) []string {
	out := make([]string, 0, len(MemoryFiles()))
	for _, name := range MemoryFiles() {
		if _, err := os.Stat(filepath.Join(projectRoot, name)); err == nil {
			out = append(out, name+"  ✅")
			continue
		}
		out = append(out, name+"  ⚠️ 缺失（建议补上：它是 agent 读项目约定的入口）")
	}
	return out
}

// RelativePaths 把项目内的相对路径转成 git 能用的形式（统一用 `/`）。
func RelativePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepath.ToSlash(strings.TrimSpace(p)))
	}
	return out
}
