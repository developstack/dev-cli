// Command dev-cli 用团队在平台上配置的模型、MCP 与技能启动编码 agent（pi 优先，Claude Code 为辅）。
//
// 设计见 ADR-0025 / 0026 与设计稿《dev-cli 2.0》；用法：dev-cli help。
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/developstack/aidevstack/cli/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := (&cli.App{}).Run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
