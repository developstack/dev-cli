# dev-cli

aidevstack 平台的本地编码 agent 启动器：登录平台、按 git 仓库匹配平台项目、把项目的模型 / 技能 / MCP 渲染进 agent 的
托管配置目录，然后启动 **pi**（主力）或 **Claude Code**。

> 本仓库（`developstack/dev-cli`）是平台主仓 `cli/` 目录的**发布镜像**：每次发版由主仓 CI 推送一个快照并打 tag `v<semver>`，
> 本仓工作流据此测试、构建并发布 [Releases](https://github.com/developstack/dev-cli/releases)。请不要直接向本仓提交改动。

## 安装

### Homebrew（macOS / Linux）

```bash
brew install developstack/tap/dev-cli
brew upgrade dev-cli
```

### 安装脚本（macOS / Linux）

```bash
curl -fsSL https://github.com/developstack/dev-cli/releases/latest/download/install.sh | sh

# 指定版本 / 安装目录
curl -fsSL https://github.com/developstack/dev-cli/releases/latest/download/install.sh | sh -s -- --version 2.0.2
curl -fsSL https://github.com/developstack/dev-cli/releases/latest/download/install.sh | sh -s -- --dir ~/bin
```

脚本识别平台，匿名下载对应的包与 `checksums.txt`，校验 sha256 后装进 PATH 里的目录（默认 `~/.local/bin`），不需要任何令牌。

### 手动下载（Windows 用这个）

从 [Releases](https://github.com/developstack/dev-cli/releases) 下载对应平台的包和 `checksums.txt`：

| 平台 | 文件 |
|---|---|
| macOS (Apple Silicon) | `dev-cli_darwin_arm64.tar.gz` |
| macOS (Intel) | `dev-cli_darwin_amd64.tar.gz` |
| Linux (x86_64) | `dev-cli_linux_amd64.tar.gz` |
| Linux (arm64) | `dev-cli_linux_arm64.tar.gz` |
| Windows (x86_64) | `dev-cli_windows_amd64.zip` |
| Windows (arm64) | `dev-cli_windows_arm64.zip` |

```bash
shasum -a 256 -c checksums.txt --ignore-missing        # Linux 用 sha256sum -c checksums.txt --ignore-missing
tar -xzf dev-cli_darwin_arm64.tar.gz && install -m 0755 dev-cli ~/.local/bin/dev-cli
```

Windows：`Get-FileHash dev-cli_windows_amd64.zip -Algorithm SHA256` 与 `checksums.txt` 对照后解压，把 `dev-cli.exe` 放进 `PATH`。

## 开始使用

```bash
dev-cli login --platform https://<平台>     # 打开浏览器批准（设备授权），凭据存进系统钥匙串
cd <已在平台登记的项目仓库>
dev-cli start pi                            # 或 dev-cli start claude；`--` 之后的参数原样交给 agent
dev-cli doctor                              # 排查环境
```

## 与平台的关系

- dev-cli 是平台的本地唯一入口：开发者密钥按「用户 × 项目 × 设备」自动签发、定期轮换，只经环境变量交给 agent 进程；
  agent 配置写进托管目录，不读取你的 `~/.claude` / `~/.pi`。
- 模型、技能（已审核版本）与 MCP 工具都由平台按项目下发；在项目仓库里按 git remote 自动匹配平台项目。
- 平台可设置最低 CLI 版本：版本过低时命令会提示升级（`brew upgrade dev-cli` 或重新运行安装脚本）。

## 从源码构建

```bash
go build -o dev-cli ./cmd/dev-cli
go test ./...
```

Go 模块路径是 `github.com/developstack/aidevstack/cli`（与主仓一致），因此不支持 `go install github.com/developstack/dev-cli@…`，
请用上面的安装方式。

许可证：MIT（见 [LICENSE](LICENSE)）。
