package agents

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// EnsureInstalled 检查工具是否已安装；没装就问用户要不要装。
//
// 学习点（为什么要在 init / start 两处都查）：auth 注入的前提是"工具真的存在" ——
// 到 `dev-cli start` 才发现没装，用户已经白等一轮。所以 init 时先体检、start 时兜底。
//
// 参数 in 可为 nil（非交互场景：只提示、不安装）。
func EnsureInstalled(in io.Reader, out io.Writer, a Agent) (string, bool, error) {
	if path, ok := a.Detect(); ok {
		return path, true, nil
	}

	fmt.Fprintf(out, "⚠️  没有找到 %s（命令 %q 不在 PATH 里）\n", a.Name, a.Binary)
	cmd := a.InstallCommand()
	fmt.Fprintf(out, "   安装命令：%s\n", strings.Join(cmd, " "))

	if in == nil {
		fmt.Fprintf(out, "   装好后重试：dev-cli start %s\n", a.ID)
		return "", false, nil
	}

	fmt.Fprint(out, "   现在安装吗？[y/N] ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return "", false, nil
	}
	if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
		fmt.Fprintf(out, "   已跳过。装好后重试：dev-cli start %s\n", a.ID)
		return "", false, nil
	}

	fmt.Fprintf(out, "   → 执行 %s\n", strings.Join(cmd, " "))
	run := exec.Command(cmd[0], cmd[1:]...)
	run.Stdout, run.Stderr = out, out
	if err := run.Run(); err != nil {
		return "", false, fmt.Errorf("dev-cli: 安装 %s 失败: %w", a.Name, err)
	}
	if path, ok := a.Detect(); ok {
		fmt.Fprintf(out, "   ✅ 已安装：%s\n", path)
		return path, true, nil
	}
	// 装完还找不到：多半是新装的可执行文件不在当前 PATH（要重开终端）。
	fmt.Fprintf(out, "   ⚠️  装好了但 PATH 里还找不到，请重开终端后再试\n")
	return "", false, nil
}
