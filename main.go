// Command dev-cli 把平台的项目配置（技能等）同步到本地 AI 编码工具。
//
// 设计要点：
//   - **零外部依赖**：只用标准库 —— 交叉编译三端毫无悬念，也没有供应链风险；
//   - **凭据放项目里**（`.dev-cli/settings.json`，0600，且被 .gitignore 挡住）：
//     绑定关系是项目属性，换机器重新 init 即可；
//   - **未认证是正常状态**：没有设置文件时提示"请先 dev-cli init"，而不是抛栈。
package main

import (
	"os"

	"github.com/developstack/dev-cli/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
