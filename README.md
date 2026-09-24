# dev-cli

把 aidevstack 平台上的**项目配置**（技能等）同步到你本地的 AI 编码工具。

```
dev-cli init --apikey=<平台 API Key>     # 初始化：认证 → 选项目 → 建骨架 → 同步技能
dev-cli sync                             # 与平台对账（拉取变更并落地）
dev-cli status                           # 看当前认证与已装技能
```

## 安装

**① 一行命令（macOS / Linux）** —— 自动识别平台，装进 PATH 里的目录

```bash
curl -fsSL https://raw.githubusercontent.com/developstack/dev-cli/main/install.sh | sh

# 指定版本 / 指定目录
curl -fsSL .../install.sh | sh -s -- --version v0.1.2
curl -fsSL .../install.sh | sh -s -- --dir ~/bin
```

**② Go 用户**

```bash
go install github.com/developstack/dev-cli@latest
# 装到 $(go env GOPATH)/bin —— 记得把它加进 PATH：
export PATH="$HOME/go/bin:$PATH"
```

**③ 手动下载**（Windows 用这个）

从 [Releases](../../releases) 下载对应平台的包，解压后把可执行文件放进 `PATH`：

| 平台 | 文件 |
|---|---|
| macOS (Apple Silicon) | `dev-cli_darwin_arm64.tar.gz` |
| macOS (Intel) | `dev-cli_darwin_amd64.tar.gz` |
| Linux (x86_64) | `dev-cli_linux_amd64.tar.gz` |
| Linux (arm64) | `dev-cli_linux_arm64.tar.gz` |
| Windows (x86_64) | `dev-cli_windows_amd64.zip` |
| Windows (arm64) | `dev-cli_windows_arm64.zip` |

**验证**

```bash
dev-cli version      # → dev-cli v0.1.2
```

> **`command not found`？** 说明装到了不在 `PATH` 的目录。`install.sh` 会明确告诉你该加哪一行；
> 手动安装的话把二进制挪进 `~/.local/bin`、`/opt/homebrew/bin` 或 `/usr/local/bin` 之一即可。

## 快速开始

```bash
cd <你的项目>
dev-cli init --apikey=devkey
```

它会做这几件事：

```
1. 用 API Key 拉取「你能绑定的项目」，让你选一个
2. 建目录骨架：
     .dev-cli/{skills,MCP,rules,design,docs}
     .pi/skills  .claude/skills  .codex/skills  .cursor/skills
3. 检查 AGENTS.md / CLAUDE.md（只报告，不自动生成）
4. 把平台该项目启用的技能同步到上面每个工具目录
5. 更新 .gitignore（同步产物与凭据不入库）；
   已经跟踪过的，自动 `git rm --cached` 解除跟踪（**工作区文件保留**）
6. 把凭据存到 .dev-cli/settings.json（0600，已被 .gitignore 挡住）
```

之后：

```bash
dev-cli sync      # 平台改了技能配置 → 本地一条命令对齐
dev-cli status    # 看当前绑定与已装技能
```

> 没初始化就跑 `sync`/`status` 会提示：
> `未认证：没有找到 .dev-cli/settings.json。请先运行：dev-cli init --apikey=<平台 API Key>`

## 设计说明

**凭据放在项目里**（`.dev-cli/settings.json`，0600）

绑定关系是**项目属性**（"这个项目属于平台上的哪个项目"），放项目里换机器只要重新 init 一次；
放用户主目录会变成"一台机器只能绑一个项目"。

**技能落三处**

```
.dev-cli/skills/<slug>/     本地真源（上报"已装什么"读它）
.pi/skills/<slug>/          各工具真正读的地方
.claude/skills/<slug>/
.codex/skills/<slug>/
.cursor/skills/<slug>/
```

上报只读**本地真源**，不扫工具目录 —— 否则你自己放进 `.claude/skills` 的技能会被平台
当成"平台装的"，进而按它的有效集把不该删的删掉。

**为什么 `.dev-cli/rules|design|docs|MCP` 不入 .gitignore**

那是**团队自己写**的资产，应当入库；只有 `skills/`（平台下发的副本）和 `settings.json`（凭据）被忽略。

**零外部依赖**

只用 Go 标准库 —— 交叉编译三端毫无悬念，也没有供应链风险。

## 开发

```bash
go build -o dev-cli .
go test ./...
go vet ./...
```

发布：推一个 `v*` tag，GitHub Actions 会自动构建六种组合并挂到 Release。

```bash
git tag v0.1.0 && git push origin v0.1.0
```

## 相关

- 平台侧契约：`backend/internal/distribution`（`/api/local-agent/*`、`/api/projects/mine`）
- 技能分发包格式：单技能 zip，**根目录含 `SKILL.md`**
