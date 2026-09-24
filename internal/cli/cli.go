// Package cli 实现 dev-cli 的命令行入口。
//
// 学习点：命令分发用标准库 `flag` 手写，**不引第三方 CLI 框架** ——
// 三个子命令不值得为它背一个依赖；而零依赖让交叉编译（三端发布）变得毫无悬念。
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/developstack/dev-cli/internal/config"
	"github.com/developstack/dev-cli/internal/platform"
)

// Version 是构建时注入的版本（见 .github/workflows/release.yml）。
var Version = "dev"

// resolvedVersion 返回可展示的版本号。
//
// 学习点：`go install ...@vX.Y.Z` 走的是标准构建，没有我们的 ldflags 注入 ——
// 直接从模块信息里取版本，用户看到的就是真实版本而不是 "dev"。
func resolvedVersion() string {
	if Version != "" && Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return Version
}

// Run 执行一次命令；返回进程退出码。
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stdout)
		return 0
	}
	ctx := context.Background()
	switch args[0] {
	case "init":
		return runInit(ctx, args[1:], stdout, stderr)
	case "sync":
		return runSync(ctx, args[1:], stdout, stderr)
	case "status":
		return runStatus(ctx, args[1:], stdout)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "dev-cli %s\n", resolvedVersion())
		return 0
	case "help", "--help", "-h":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "未知命令：%s\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

// printUsage 打印用法。
func printUsage(w io.Writer) {
	fmt.Fprint(w, `dev-cli —— 把平台的项目配置同步到本地 AI 编码工具

用法：
  dev-cli init --apikey=<平台 API Key> [--endpoint=<平台地址>] [--project=<项目 id>]
      初始化：校验密钥 → 选项目 → 建目录骨架 → 同步技能 → 配 .gitignore → 存凭据

  dev-cli sync
      与平台对账：拉取待执行命令并安装/卸载技能（没初始化会提示先 init）

  dev-cli status
      显示当前认证与已装技能状态

  dev-cli version
      显示版本

常用参数：
  --endpoint  平台根地址（默认 `+platform.DefaultEndpoint+`，也可用 DEV_CLI_ENDPOINT）
  --dir       项目根目录（默认当前目录）
  --project   直接指定项目 id（跳过交互选择）
`)
}

// options 是各命令共用的参数。
type options struct {
	endpoint string
	dir      string
}

// parseCommon 解析公共参数（返回剩余参数）。
func parseCommon(fs *flag.FlagSet, env func(string) string) options {
	var o options
	fs.StringVar(&o.endpoint, "endpoint", env("DEV_CLI_ENDPOINT"), "平台根地址")
	fs.StringVar(&o.dir, "dir", "", "项目根目录（默认当前目录）")
	return o
}

// projectRoot 解析项目根目录（绝对路径）。
func projectRoot(dir string) (string, error) {
	target := strings.TrimSpace(dir)
	if target == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("dev-cli: 取当前目录失败: %w", err)
		}
		target = cwd
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("dev-cli: 解析目录失败: %w", err)
	}
	return abs, nil
}

// requireAuth 读取设置；未初始化时给出**明确的下一步**。
//
// 学习点：未认证是**正常状态**，提示里必须带"该敲什么命令" ——
// 只说"未认证"等于把问题丢回给用户。
func requireAuth(root string) (config.Settings, error) {
	settings, err := config.Load(root)
	if errors.Is(err, config.ErrNotInitialized) {
		return config.Settings{}, fmt.Errorf(
			"未认证：没有找到 %s。\n请先运行：dev-cli init --apikey=<平台 API Key>",
			filepath.Join(config.DirName, config.FileName))
	}
	return settings, err
}

// chooseProject 让用户从可见项目里选一个。
func chooseProject(in io.Reader, out io.Writer, projects []platform.Project, want string) (platform.Project, error) {
	if len(projects) == 0 {
		return platform.Project{}, errors.New(
			"这把密钥看不到任何项目（可能它所属的用户没有团队）。请联系管理员把用户加入团队，或换一把密钥")
	}
	if want = strings.TrimSpace(want); want != "" {
		for _, p := range projects {
			if p.ID == want {
				return p, nil
			}
		}
		return platform.Project{}, fmt.Errorf("项目 %q 不在你的可见范围内", want)
	}

	fmt.Fprintln(out, "\n可绑定的项目：")
	for i, p := range projects {
		fmt.Fprintf(out, "  %d) %s  [id=%s]\n", i+1, p.Name, p.ID)
	}
	fmt.Fprintf(out, "\n选择项目编号（1-%d）：", len(projects))

	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return platform.Project{}, fmt.Errorf("读取选择失败: %w", err)
	}
	index, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || index < 1 || index > len(projects) {
		return platform.Project{}, errors.New("无效的项目编号")
	}
	return projects[index-1], nil
}
