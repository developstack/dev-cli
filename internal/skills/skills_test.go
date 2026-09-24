package skills

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// buildZip 造一个 zip（键是包内路径）。
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// TestInstallStripsOuterDir 包里有外层目录时，装出来**不能多一层**。
//
// 学习点：打包方式不统一（有的把技能目录打进去，有的直接打内容），
// 所以安装要以"SKILL.md 所在的那一层"为根 —— 否则工具读不到 SKILL.md。
func TestInstallStripsOuterDir(t *testing.T) {
	root := t.TempDir()
	pkg := buildZip(t, map[string]string{
		"xlsx/SKILL.md":       "---\nname: xlsx\n---\n正文\n",
		"xlsx/scripts/run.py": "print(1)\n",
		"unrelated/other.txt": "丢弃\n",
	})

	if err := Install(root, "xlsx", pkg); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(LocalDir(root), "xlsx", "SKILL.md")); err != nil {
		t.Fatalf("本地真源缺少 SKILL.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(LocalDir(root), "xlsx", "scripts", "run.py")); err != nil {
		t.Fatalf("子目录文件没装进来: %v", err)
	}
	if _, err := os.Stat(filepath.Join(LocalDir(root), "xlsx", "xlsx")); err == nil {
		t.Fatal("不该出现多一层 xlsx/xlsx")
	}
	if _, err := os.Stat(filepath.Join(LocalDir(root), "xlsx", "unrelated")); err == nil {
		t.Fatal("技能目录之外的文件应当丢弃")
	}
}

// TestInstallRejectsPackageWithoutSkillMD 包里没有 SKILL.md 要明确报错。
func TestInstallRejectsPackageWithoutSkillMD(t *testing.T) {
	root := t.TempDir()
	pkg := buildZip(t, map[string]string{"readme.txt": "no skill here"})
	if err := Install(root, "broken", pkg); err == nil {
		t.Fatal("没有 SKILL.md 应当报错")
	}
}

// TestInstalledReadsLocalSource 上报的"已装"只读本地真源，不扫工具目录。
//
// 学习点：用户自己放进 `.claude/skills` 的技能不该被算作"平台装的"，
// 否则平台会按自己的有效集把它卸载掉。
func TestInstalledReadsLocalSource(t *testing.T) {
	root := t.TempDir()
	if err := Install(root, "xlsx", buildZip(t, map[string]string{"SKILL.md": "x"})); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	// 用户自己塞进工具目录的技能。
	own := filepath.Join(root, ".claude", "skills", "my-own")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got := Installed(root)
	if len(got) != 1 || got[0] != "xlsx" {
		t.Fatalf("Installed() = %v, want 只有 [xlsx]", got)
	}
}

// TestUninstallRemovesEverywhere 卸载要把所有位置都清掉。
func TestUninstallRemovesEverywhere(t *testing.T) {
	root := t.TempDir()
	if err := Install(root, "xlsx", buildZip(t, map[string]string{"SKILL.md": "x"})); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if err := Uninstall(root, "xlsx"); err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	for _, dir := range append([]string{LocalDir(root)}, agentDirs(root)...) {
		if _, err := os.Stat(filepath.Join(dir, "xlsx")); err == nil {
			t.Fatalf("%s 下还留着 xlsx", dir)
		}
	}
}
