package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureGitignoreIsIdempotent 反复执行只留**一份**托管块。
//
// 学习点：往别人的 .gitignore 追加内容必须有边界 —— 否则跑三次 init 就留三份重复条目，
// 用户手写的规则也会被搅乱。
func TestEnsureGitignoreIsIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := EnsureGitignore(root); err != nil {
			t.Fatalf("第 %d 次 EnsureGitignore() error = %v", i+1, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(raw)
	if n := strings.Count(content, beginMarker); n != 1 {
		t.Fatalf("托管块出现 %d 次，want 1\n%s", n, content)
	}
	if !strings.Contains(content, "node_modules/") {
		t.Fatal("用户原有的规则被弄丢了")
	}
	for _, pattern := range IgnorePatterns() {
		if !strings.Contains(content, pattern) {
			t.Fatalf("缺少忽略项 %q\n%s", pattern, content)
		}
	}
}

// TestIgnorePatternsCoverKeyAndSkills 凭据与同步产物必须被忽略，团队资产不忽略。
func TestIgnorePatternsCoverKeyAndSkills(t *testing.T) {
	joined := strings.Join(IgnorePatterns(), "\n")
	for _, want := range []string{".dev-cli/settings.json", ".dev-cli/skills/", ".claude/skills/"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("应当忽略 %q：\n%s", want, joined)
		}
	}
	// 团队自己写的资产要入库，不能忽略。
	for _, notWant := range []string{".dev-cli/rules", ".dev-cli/docs", ".dev-cli/design"} {
		if strings.Contains(joined, notWant) {
			t.Fatalf("不该忽略团队资产 %q：\n%s", notWant, joined)
		}
	}
}

// TestCheckMemoryFilesReportsMissing 缺 AGENTS.md/CLAUDE.md 要如实报出来（不自动创建）。
func TestCheckMemoryFilesReportsMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	lines := CheckMemoryFiles(root)
	if len(lines) != 2 {
		t.Fatalf("应当报 2 个文件，得到 %v", lines)
	}
	if !strings.Contains(lines[0], "✅") {
		t.Fatalf("AGENTS.md 存在应标 ✅：%s", lines[0])
	}
	if !strings.Contains(lines[1], "缺失") {
		t.Fatalf("CLAUDE.md 缺失应标出来：%s", lines[1])
	}
}
