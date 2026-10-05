#!/bin/sh
# dev-cli 安装脚本：识别平台 → 从 GitHub Release 下载 → 校验 sha256 → 装进 PATH。
#
# 默认源是公开仓 developstack/dev-cli（只放二进制发布，tag v<semver>，由主仓 cli-release.yml 同步），匿名即可下载：
#   curl -fsSL https://github.com/developstack/dev-cli/releases/latest/download/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --version 2.0.1     # 指定版本
#   curl -fsSL .../install.sh | sh -s -- --dir ~/bin         # 指定安装目录
#
# 改用别的源（例如私有的主仓或镜像仓）时用环境变量覆盖：
#   DEV_CLI_REPO        源仓 owner/name（默认 developstack/dev-cli）
#   DEV_CLI_TAG_PREFIX  版本 tag 前缀（默认 v；主仓是 cli/v）
#   DEV_CLI_TOKEN       对源仓有读权限的令牌；设置后改走 GitHub API 的资产端点（私有仓的 releases/download/ 地址带令牌也是 404）
# 例：DEV_CLI_REPO=developstack/aidevstack DEV_CLI_TAG_PREFIX=cli/v DEV_CLI_TOKEN=$(gh auth token) sh install.sh
#
# 令牌只认 DEV_CLI_TOKEN，不读环境里现成的 GITHUB_TOKEN / GH_TOKEN：默认源是公开仓，不该把无关令牌发出去，
# 而且一个过期的 GITHUB_TOKEN 会让本可匿名成功的下载变成 401。
#
# 也可以：brew install developstack/tap/dev-cli
set -eu

REPO="${DEV_CLI_REPO:-developstack/dev-cli}"
TAG_PREFIX="${DEV_CLI_TAG_PREFIX:-v}"
TOKEN="${DEV_CLI_TOKEN:-}"
BIN="dev-cli"
VERSION=""
INSTALL_DIR="${INSTALL_DIR:-}"

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="${2#v}"; shift 2 ;;
    --dir)     INSTALL_DIR="$2"; shift 2 ;;
    -h|--help)
      echo "usage: install.sh [--version X.Y.Z] [--dir <install dir>]"
      echo "env:   DEV_CLI_REPO (default developstack/dev-cli), DEV_CLI_TAG_PREFIX (default v), DEV_CLI_TOKEN"
      exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin|linux) ;;
  msys*|mingw*|cygwin*)
    echo "On Windows download dev-cli_windows_<arch>.zip from https://github.com/$REPO/releases" >&2
    exit 1 ;;
  *) echo "unsupported OS: $os" >&2; exit 1 ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac

# GitHub API 请求（只在设置了 DEV_CLI_TOKEN 时使用）。
api() {
  curl -fsSL -H "Authorization: Bearer $TOKEN" -H "Accept: application/vnd.github+json" "$@"
}

# 最新版本取源仓的 "latest release"（GitHub 自动排除草稿与预发布）。匿名时读 releases/latest 的跳转地址，
# 不占 API 的匿名限额；带令牌时读 API。源仓的 latest 不是 dev-cli 发布（例如主仓里有别的发布）就要求显式 --version。
if [ -z "$VERSION" ]; then
  if [ -z "$TOKEN" ]; then
    latest=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" | sed 's#.*/tag/##') || latest=""
  else
    latest_json=$(api "https://api.github.com/repos/$REPO/releases/latest") || latest_json=""
    latest=$(printf '%s\n' "$latest_json" | grep -o '"tag_name": *"[^"]*"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)"/\1/')
  fi
  latest=$(printf '%s' "$latest" | sed 's#%2F#/#g')
  case "$latest" in
    "$TAG_PREFIX"[0-9]*) VERSION="${latest#"$TAG_PREFIX"}" ;;
    *) echo "could not determine the latest dev-cli release of $REPO (got '${latest:-nothing}'); pass --version X.Y.Z" >&2
       [ -n "$TOKEN" ] || echo "private repository? set DEV_CLI_TOKEN (see the header of this script)" >&2
       exit 1 ;;
  esac
fi

if [ -z "$INSTALL_DIR" ]; then
  for candidate in "$HOME/.local/bin" "/opt/homebrew/bin" "/usr/local/bin"; do
    if [ -d "$candidate" ] && [ -w "$candidate" ]; then INSTALL_DIR="$candidate"; break; fi
  done
  [ -n "$INSTALL_DIR" ] || INSTALL_DIR="$HOME/.local/bin"
fi
mkdir -p "$INSTALL_DIR"

tag="${TAG_PREFIX}${VERSION}"
tag_url=$(printf '%s' "$tag" | sed 's#/#%2F#g')   # 主仓的 cli/v2.0.1 在 URL 里写作 cli%2Fv2.0.1
name="${BIN}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# fetch <资产名> <目标文件>。无令牌走公开下载地址；有令牌走 API 资产端点 —— 私有仓只能按资产 id 以
# Accept: application/octet-stream 取（302 到签名地址，curl 跳转时不转发令牌）。
release_json=""
fetch() {
  if [ -z "$TOKEN" ]; then
    curl -fsSL "https://github.com/$REPO/releases/download/$tag_url/$1" -o "$2"
    return
  fi
  if [ -z "$release_json" ]; then
    release_json=$(api "https://api.github.com/repos/$REPO/releases/tags/$tag_url") || return 1
  fi
  # 不依赖 jq：每个资产对象里 url（…/releases/assets/<id>）先于 name 出现，按此配对。
  asset_url=$(printf '%s\n' "$release_json" | tr ',' '\n' | awk -v want="$1" '
    /"url": *"https:\/\/api\.github\.com\/repos\/[^"]*\/releases\/assets\/[0-9]+"/ {
      u = $0; sub(/.*"url": *"/, "", u); sub(/".*/, "", u) }
    /"name": *"/ {
      n = $0; sub(/.*"name": *"/, "", n); sub(/".*/, "", n)
      if (n == want && u != "") { print u; exit } }')
  [ -n "$asset_url" ] || return 1
  curl -fsSL -H "Authorization: Bearer $TOKEN" -H "Accept: application/octet-stream" "$asset_url" -o "$2"
}

echo "-> downloading dev-cli $VERSION ($os/$arch) from $REPO"
fetch "$name" "$tmp/$name" || { echo "download failed: $name ($REPO release $tag)" >&2; exit 1; }
fetch checksums.txt "$tmp/checksums.txt" || { echo "download failed: checksums.txt ($REPO release $tag)" >&2; exit 1; }

# 校验 sha256：发布物与 checksums.txt 一起生成，不一致就拒绝安装。
expected=$(grep " $name\$" "$tmp/checksums.txt" | awk '{print $1}')
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$name" | awk '{print $1}')
else
  actual=$(shasum -a 256 "$tmp/$name" | awk '{print $1}')
fi
if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
  echo "checksum mismatch for $name (expected ${expected:-none}, got $actual)" >&2
  exit 1
fi
echo "-> sha256 verified ($actual)"

tar -xzf "$tmp/$name" -C "$tmp"
install -m 0755 "$tmp/$BIN" "$INSTALL_DIR/$BIN"
echo "installed $INSTALL_DIR/$BIN"
"$INSTALL_DIR/$BIN" version || true

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo; echo "$INSTALL_DIR is not on your PATH; add this to your shell profile:"
     echo "    export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
esac
echo
echo "Next: dev-cli login --platform https://<your-platform>   then, in a registered repository: dev-cli start pi"
