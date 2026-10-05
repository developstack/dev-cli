package auth

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
)

// Headless 报告当前会话是否没有图形界面可用（SSH 会话、Linux 没有 DISPLAY / WAYLAND_DISPLAY，或用户显式关闭）。
func Headless() bool {
	if os.Getenv("AIDEVSTACK_NO_BROWSER") == "1" {
		return true
	}
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return true
	}
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return true
	}
	return false
}

// OpenURL 用系统默认浏览器打开 http(s) 链接（open / xdg-open / rundll32）。
//
// 只接受 http / https：链接来自平台响应，不可信，不能让它变成"打开任意本地文件或协议处理器"。
func OpenURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("refusing to open non-http URL %q", raw)
	}
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{u.String()}
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", u.String()}
	default:
		name, args = "xdg-open", []string{u.String()}
	}
	// 不用 CommandContext：打开器（open / xdg-open）会立即返回或常驻，dev-cli 只管启动，不等它，
	// 也不能在函数返回时把它杀掉。Wait 放进 goroutine 回收子进程。
	//nolint:noctx // 打开器只启动不等待（见上），不受调用方 context 约束。
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
