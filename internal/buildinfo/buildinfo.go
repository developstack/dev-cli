// Package buildinfo 是 dev-cli 的版本信息（发布时由 -ldflags -X 注入）。
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// 由 cli-release 工作流注入（-ldflags -X github.com/developstack/aidevstack/cli/internal/buildinfo.Version=2.0.0）。
var (
	// Version 语义化版本（不带 v）；本地构建为 "dev"（服务端的 426 闸门对解析不了的版本放行）。
	Version = "dev"
	// Commit 构建的 git 提交。
	Commit = ""
	// Date 构建时间（RFC 3339）。
	Date = ""
)

// CurrentVersion 返回版本号：注入的优先；`go install …@v2.x.y` 安装的二进制没有注入，取模块版本。
func CurrentVersion() string {
	if Version != "dev" {
		return strings.TrimPrefix(Version, "v")
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return strings.TrimPrefix(v, "v")
		}
	}
	return Version
}

// UserAgent 是每个请求的 User-Agent（服务端据此做最低版本闸门与统计）。
func UserAgent() string {
	return fmt.Sprintf("dev-cli/%s (%s; %s)", CurrentVersion(), runtime.GOOS, runtime.GOARCH)
}

// String 是 `dev-cli version` 的输出。
func String() string {
	out := "dev-cli " + CurrentVersion()
	if Commit != "" {
		out += " (" + Commit
		if Date != "" {
			out += ", " + Date
		}
		out += ")"
	}
	return out + " " + runtime.GOOS + "/" + runtime.GOARCH
}
