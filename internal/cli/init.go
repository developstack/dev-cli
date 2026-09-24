package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/developstack/dev-cli/internal/agents"
	"github.com/developstack/dev-cli/internal/config"
	"github.com/developstack/dev-cli/internal/platform"
	"github.com/developstack/dev-cli/internal/project"
)

// runInit 实现 `dev-cli init`。
func runInit(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	common := parseCommon(fs, os.Getenv)
	apiKey := fs.String("apikey", "", "平台 API Key（必填）")
	wantProject := fs.String("project", "", "直接指定项目 id（跳过交互选择）")
	skipSync := fs.Bool("no-sync", false, "只初始化，不同步技能")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *apiKey == "" {
		fmt.Fprintln(stderr, "缺少 --apikey=<平台 API Key>")
		return 2
	}

	root, err := projectRoot(common.dir)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "项目目录：%s\n平台地址：%s\n", root, platform.New(common.endpoint, *apiKey).Endpoint())

	client := platform.New(common.endpoint, *apiKey)
	projects, err := client.Projects(ctx)
	if err != nil {
		return fail(stderr, errors.Join(errors.New("校验 API Key 失败"), err))
	}
	fmt.Fprintf(stdout, "认证成功：可见 %d 个项目\n", len(projects))

	chosen, err := chooseProject(os.Stdin, stdout, projects, *wantProject)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "已选择项目：%s [id=%s]\n", chosen.Name, chosen.ID)

	if err := prepareLayout(stdout, root); err != nil {
		return fail(stderr, err)
	}
	checkAgents(stdout)

	settings := config.Settings{
		APIKey: *apiKey, Endpoint: client.Endpoint(),
		ProjectID: chosen.ID, ProjectName: chosen.Name,
		AgentID: newAgentID(), CreatedAt: time.Now().UTC(),
	}
	if err := config.Save(root, settings); err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "凭据已保存：%s（0600，已被 .gitignore 挡住）\n", filepath.Join(config.DirName, config.FileName))

	if *skipSync {
		fmt.Fprintln(stdout, "\n初始化完成（按要求跳过了技能同步）。")
		return 0
	}
	fmt.Fprintln(stdout, "\n开始同步技能…")
	return runSync(ctx, []string{"--dir", root, "--endpoint", client.Endpoint()}, stdout, stderr)
}

// checkAgents 体检各 agent 是否已安装；缺的就问要不要现在装。
//
// 学习点：**在 init 就查** —— 等到 `dev-cli start` 才发现没装，用户已经白等一轮。
// 而已安装的直接报路径，让用户确认"等下启动的就是它"。
func checkAgents(stdout io.Writer) {
	fmt.Fprintln(stdout, "\nAI 编码工具体检：")
	for _, a := range agents.All() {
		if path, ok := a.Detect(); ok {
			fmt.Fprintf(stdout, "  %-12s ✅ %s\n", a.Name, path)
			continue
		}
		fmt.Fprintf(stdout, "  %-12s ⚠️  未安装\n", a.Name)
		if _, ready, err := agents.EnsureInstalled(os.Stdin, stdout, a); err != nil {
			fmt.Fprintf(stdout, "     安装失败：%v\n", err)
		} else if !ready {
			fmt.Fprintf(stdout, "     之后可用 dev-cli start %s 再试\n", a.ID)
		}
	}
}

// prepareLayout 建目录骨架、检查说明文件、配置 git 忽略。
func prepareLayout(stdout io.Writer, root string) error {
	if err := project.EnsureLayout(root); err != nil {
		return err
	}
	if err := agents.EnsureDirs(root); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "目录骨架已就绪：%s/{%s}\n",
		config.DirName, join(project.SubDirs()))
	fmt.Fprintf(stdout, "工具目录已就绪：%s\n", join(agents.SkillDirs()))

	fmt.Fprintln(stdout, "\n项目说明文件检查：")
	for _, line := range project.CheckMemoryFiles(root) {
		fmt.Fprintf(stdout, "  %s\n", line)
	}

	if err := project.EnsureGitignore(root); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "\n.gitignore 已更新（同步产物与凭据不入库）")
	return untrackSynced(stdout, root)
}

// untrackSynced 把已跟踪的同步产物从索引里摘掉。
//
// 学习点：只在"确实被跟踪"时才动 git —— 否则每个项目都会打印一堆无意义的 git 操作，
// 而 `git rm` 对未跟踪路径本来也会报错（虽然有 --ignore-unmatch）。
func untrackSynced(stdout io.Writer, root string) error {
	if !project.InGitRepo(root) {
		fmt.Fprintln(stdout, "（当前目录不是 git 仓库，跳过解除跟踪）")
		return nil
	}
	patterns := project.IgnorePatterns()
	tracked := project.TrackedPaths(root, patterns)
	if len(tracked) == 0 {
		fmt.Fprintln(stdout, "没有已跟踪的同步产物需要解除跟踪")
		return nil
	}
	if err := project.Untrack(root, tracked); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "已解除跟踪（工作区文件保留）：%s\n", join(tracked))
	return nil
}

// newAgentID 生成本机标识。
func newAgentID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("agent-%d", time.Now().UnixNano())
	}
	return "agent-" + hex.EncodeToString(buf[:])
}

// join 用逗号连接（输出友好）。
func join(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ", "
		}
		out += item
	}
	return out
}

// fail 打印错误并返回退出码。
func fail(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "✗ %s\n", err)
	return 1
}
