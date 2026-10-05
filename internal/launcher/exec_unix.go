//go:build !windows

package launcher

import (
	"fmt"
	"syscall"
)

// Exec 用 agent 替换当前进程（syscall.Exec）：信号、终端与退出码天然属于 agent，dev-cli 不再存在。
// 成功时不返回。
func Exec(binary string, argv, env []string) error {
	if err := syscall.Exec(binary, argv, env); err != nil {
		return fmt.Errorf("exec %s: %w", binary, err)
	}
	return nil
}
