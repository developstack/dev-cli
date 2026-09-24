#!/bin/sh
# dev-cli 安装脚本：自动识别平台 → 从 GitHub Release 下载 → 装进 PATH。
#
# 用法：
#   curl -fsSL https://raw.githubusercontent.com/developstack/dev-cli/main/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --version v0.1.1     # 指定版本
#   curl -fsSL .../install.sh | sh -s -- --dir ~/bin          # 指定安装目录
#
# 学习点：为什么要有脚本 —— Release 里给的是压缩包，用户还得自己解压、chmod、挪进 PATH；
# "复制一行命令就能用"是 CLI 工具的基本盘。
set -eu

REPO="developstack/dev-cli"
BIN="dev-cli"
VERSION=""

# ── 参数 ──
while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --dir)     INSTALL_DIR="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,10p' "$0" 2>/dev/null || echo "用法：install.sh [--version vX.Y.Z] [--dir <安装目录>]"
      exit 0 ;;
    *) echo "未知参数：$1" >&2; exit 2 ;;
  esac
done

# ── 识别平台 ──
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin|linux) ;;
  msys*|mingw*|cygwin*)
    echo "Windows 请从 https://github.com/$REPO/releases 下载 .zip 并解压使用" >&2
    exit 1 ;;
  *) echo "不支持的平台：$os" >&2; exit 1 ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "不支持的架构：$arch" >&2; exit 1 ;;
esac

# ── 定版本 ──
if [ -z "$VERSION" ]; then
  # 学习点：从 `releases/latest` 的重定向里取 tag —— 不需要 jq，也不用 GitHub API 的速率配额。
  VERSION=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" \
    | sed 's#.*/tag/##')
  [ -n "$VERSION" ] || { echo "取最新版本失败" >&2; exit 1; }
fi

# ── 定安装目录（优先用户级、已在 PATH 的目录）──
if [ -z "${INSTALL_DIR:-}" ]; then
  for candidate in "$HOME/.local/bin" "/opt/homebrew/bin" "/usr/local/bin"; do
    if [ -d "$candidate" ] && [ -w "$candidate" ]; then INSTALL_DIR="$candidate"; break; fi
  done
  [ -n "${INSTALL_DIR:-}" ] || INSTALL_DIR="$HOME/.local/bin"
fi
mkdir -p "$INSTALL_DIR"

# ── 下载并安装 ──
name="${BIN}_${os}_${arch}"
url="https://github.com/$REPO/releases/download/$VERSION/${name}.tar.gz"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "→ 下载 $name $VERSION"
if ! curl -fsSL "$url" -o "$tmp/pkg.tar.gz"; then
  echo "✗ 下载失败：$url" >&2
  echo "  可用版本见 https://github.com/$REPO/releases" >&2
  exit 1
fi
tar xzf "$tmp/pkg.tar.gz" -C "$tmp"
install -m 0755 "$tmp/$BIN" "$INSTALL_DIR/$BIN"

echo "✅ 已安装到 $INSTALL_DIR/$BIN"
"$INSTALL_DIR/$BIN" version || true

# ── 提醒 PATH ──
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    echo
    echo "⚠️  $INSTALL_DIR 不在 PATH 里，把下面这行加到 shell 配置（~/.zshrc / ~/.bashrc）："
    echo "     export PATH=\"$INSTALL_DIR:\$PATH\""
    echo "   然后 source 一下或重开终端。" ;;
esac
