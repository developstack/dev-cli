//go:build windows

package launcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
)

// ExitError 携带 agent 的退出码（Windows 上 dev-cli 是父进程，透传它）。
type ExitError struct{ Code int }

func (e ExitError) Error() string { return fmt.Sprintf("agent exited with status %d", e.Code) }

// Exec 在 Windows 上没有 exec(2)：起子进程、继承标准输入输出，Ctrl-C 由控制台直接投递给子进程
// （这里忽略中断，避免 dev-cli 先退出把 agent 留成孤儿），结束后透传退出码。
func Exec(binary string, argv, env []string) error {
	cmd := exec.Command(binary, argv[1:]...) //nolint:gosec,noctx // G204：binary 来自 LookPath，argv 由适配器组装
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, os.Stdin, os.Stdout, os.Stderr
	signal.Ignore(os.Interrupt)
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return ExitError{Code: exit.ExitCode()}
	}
	return err
}

// ExitCode 返回 agent 的退出码（cli 据此设置 dev-cli 的退出码）。
func (e ExitError) ExitCode() int { return e.Code }
