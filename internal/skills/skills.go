// Package skills 负责技能分发包（zip）的解包与安装/卸载。
//
// 学习点（安装的目标位置）：平台下发的技能要落**三处**：
//  1. `.dev-cli/skills/<slug>/` —— 本地真源（记录"这个项目装了什么"，上报时读它）；
//  2. 各工具的项目级技能目录（`.pi/skills/<slug>` 等）—— 工具真正读的地方。
//
// 第 2 处是必须的：工具不会去 `.dev-cli` 找技能。第 1 处是为了**不依赖工具目录反推状态**
// （有些工具会自己改写目录内容）。
package skills

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/developstack/dev-cli/internal/agents"
	"github.com/developstack/dev-cli/internal/config"
)

// 限制参数。
const (
	// maxEntryBytes 单个文件解压上限。
	maxEntryBytes = 16 << 20
	// maxFiles 单个包的文件数上限。
	maxFiles = 2000
)

// ErrNoSkillFile 表示包里没有 SKILL.md。
var ErrNoSkillFile = errors.New("dev-cli: 技能包里没有 SKILL.md")

// LocalDir 返回本地真源目录（`.dev-cli/skills`）。
func LocalDir(projectRoot string) string {
	return filepath.Join(projectRoot, config.DirName, "skills")
}

// Install 把技能包解包并安装到本地真源 + 各工具的项目级技能目录。
func Install(projectRoot, slug string, pkg []byte) error {
	files, err := extract(pkg)
	if err != nil {
		return err
	}
	targets := append([]string{LocalDir(projectRoot)}, agentDirs(projectRoot)...)
	for _, dir := range targets {
		if err := writeSkill(filepath.Join(dir, slug), files); err != nil {
			return err
		}
	}
	return nil
}

// Uninstall 从所有位置移除技能。
func Uninstall(projectRoot, slug string) error {
	for _, dir := range append([]string{LocalDir(projectRoot)}, agentDirs(projectRoot)...) {
		if err := os.RemoveAll(filepath.Join(dir, slug)); err != nil {
			return fmt.Errorf("dev-cli: 移除技能 %s 失败: %w", slug, err)
		}
	}
	return nil
}

// Installed 扫描本地真源，返回已装技能（slug → 是否装了）。
//
// 学习点：上报"已装什么"读的是**本地真源**而不是工具目录 —— 工具目录里可能有
// 用户自己放的技能，混进去会让平台误判（进而下发不该有的 uninstall）。
func Installed(projectRoot string) []string {
	entries, err := os.ReadDir(LocalDir(projectRoot))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// agentDirs 返回各工具的项目级技能目录（绝对路径）。
func agentDirs(projectRoot string) []string {
	dirs := agents.SkillDirs()
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, filepath.Join(projectRoot, d))
	}
	return out
}

// writeSkill 把解包结果写到目标目录（先清空，保证是"这一版"的内容）。
func writeSkill(dest string, files map[string][]byte) error {
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("dev-cli: 清理旧技能失败: %w", err)
	}
	for name, content := range files {
		full := filepath.Join(dest, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("dev-cli: 创建目录失败: %w", err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			return fmt.Errorf("dev-cli: 写入技能文件失败: %w", err)
		}
	}
	return nil
}

// extract 解包 zip，并**剥掉外层目录**（保证 SKILL.md 在根）。
//
// 学习点：包内可能是 `xlsx/SKILL.md` 也可能是 `SKILL.md`（取决于打包方式），
// 所以先找到 SKILL.md 所在的那一层，把它的内容作为根 —— 否则装出来的目录会多一层。
func extract(pkg []byte) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(pkg), int64(len(pkg)))
	if err != nil {
		return nil, fmt.Errorf("dev-cli: 解析技能包失败: %w", err)
	}

	raw := map[string][]byte{}
	skillFile := ""
	for i, f := range reader.File {
		if i >= maxFiles {
			return nil, errors.New("dev-cli: 技能包文件数过多")
		}
		name := path.Clean(strings.TrimPrefix(filepath.ToSlash(f.Name), "./"))
		if name == "." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			continue // zip-slip 防护
		}
		if f.FileInfo().IsDir() {
			continue
		}
		content, err := readZipEntry(f)
		if err != nil {
			return nil, err
		}
		raw[name] = content
		if path.Base(name) == "SKILL.md" && (skillFile == "" || len(name) < len(skillFile)) {
			skillFile = name
		}
	}
	if skillFile == "" {
		return nil, ErrNoSkillFile
	}

	root := path.Dir(skillFile)
	out := make(map[string][]byte, len(raw))
	for name, content := range raw {
		rel := name
		if root != "." {
			trimmed, ok := strings.CutPrefix(name, root+"/")
			if !ok {
				continue // 技能目录之外的文件（如整仓打包时的其它目录）丢弃
			}
			rel = trimmed
		}
		out[rel] = content
	}
	return out, nil
}

// readZipEntry 读一个 zip 条目（带大小上限）。
func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("dev-cli: 打开包内文件失败: %w", err)
	}
	defer func() { _ = rc.Close() }()
	content, err := io.ReadAll(io.LimitReader(rc, maxEntryBytes))
	if err != nil {
		return nil, fmt.Errorf("dev-cli: 读取包内文件失败: %w", err)
	}
	return content, nil
}
