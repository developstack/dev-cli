// Package agents 描述各 AI 编码工具的**项目级**目录约定。
//
// 学习点（为什么是"项目级"）：技能分两种装法 —— 装到用户主目录（全机器可用）和装到项目里
// （只对这个项目生效）。dev-cli 走**项目级**：一个项目该有哪些技能由平台的项目配置决定，
// 所以落到项目目录里才自洽（换项目 = 换技能集）。
package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Agent 是一个 AI 编码工具。
type Agent struct {
	// ID 标识（pi / claude / codex / cursor）。
	ID string
	// Name 展示名。
	Name string
	// SkillsDir 项目级技能目录（相对项目根）。
	SkillsDir string
}

// All 返回支持的工具清单（顺序稳定，便于输出）。
//
// 学习点：目录名来自各工具自己的约定，不统一是现实 —— 硬编在这里比让用户配更省事，
// 新增工具只需加一行。
func All() []Agent {
	return []Agent{
		{ID: "pi", Name: "pi", SkillsDir: ".pi/skills"},
		{ID: "claude", Name: "Claude Code", SkillsDir: ".claude/skills"},
		{ID: "codex", Name: "Codex", SkillsDir: ".codex/skills"},
		{ID: "cursor", Name: "Cursor", SkillsDir: ".cursor/skills"},
	}
}

// SkillDirs 返回所有工具的项目级技能目录（相对项目根，去重且有序）。
func SkillDirs() []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(All()))
	for _, a := range All() {
		if _, dup := seen[a.SkillsDir]; dup {
			continue
		}
		seen[a.SkillsDir] = struct{}{}
		out = append(out, a.SkillsDir)
	}
	sort.Strings(out)
	return out
}

// EnsureDirs 创建各工具的项目级技能目录（幂等）。
func EnsureDirs(projectRoot string) error {
	for _, dir := range SkillDirs() {
		if err := os.MkdirAll(filepath.Join(projectRoot, dir), 0o755); err != nil {
			return fmt.Errorf("dev-cli: 创建 %s 失败: %w", dir, err)
		}
	}
	return nil
}
