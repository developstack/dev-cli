package repo

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// git 仓库地址的**归一化**（ADR-0025 决策 3，设计稿 §5.2）。
//
// 与 backend/internal/projects/domain/repourl.go 是**同一套规则的第二份实现**：cli/ 是独立 Go 模块，不能导入
// 后端的 internal 包；两边各读 contracts/devenv/repo-normalize.json 跑单测，向量是唯一权威，任何一边改规则
// 都必须先改向量（分叉的后果是"控制台登记了，CLI 指纹却算错"）。CLI 只用它算 remotes 指纹与展示归一化地址，
// 匹配本身在服务端。
//
// 规则：
//  1. 支持 `https://` / `http://` / `ssh://` / `git://`（可带 `user[:password]@` 与端口）以及 scp 式 `[user@]host:path`；
//     其它写法（本地路径、file://、ftp:// 等）拒绝；
//  2. 去掉 userinfo、query 与 fragment；host 转小写；去掉该协议的默认端口（https 443 / http 80 / ssh 22 /
//     git 9418），非默认端口保留为 `host:port`；
//  3. path 去掉首尾 `/` 与末尾一个 `.git`，折叠重复的 `/`，拒绝 `.` / `..` 段与空路径；
//  4. 已知大小写不敏感的托管（github.com、gitlab.com、bitbucket.org）把 path 转小写，其它主机保持原样。

// ErrInvalidRepoURL 表示仓库地址无法归一化。
var ErrInvalidRepoURL = errors.New("repo: invalid git repository url")

// ErrInvalidPathPrefix 表示子目录前缀不合法。
var ErrInvalidPathPrefix = errors.New("repo: invalid path prefix")

// maxRepoURLLen 是仓库地址的长度上限（与 devenv resolve 的单条 remote 上限一致，设计稿 §12.1）。
const maxRepoURLLen = 2048

// defaultPorts 是各协议的默认端口（归一化时去掉）。
var defaultPorts = map[string]string{"https": "443", "http": "80", "ssh": "22", "git": "9418"}

// caseInsensitiveHosts 是路径大小写不敏感的托管平台（路径统一转小写）。
var caseInsensitiveHosts = map[string]bool{"github.com": true, "gitlab.com": true, "bitbucket.org": true}

// NormalizeRepoURL 把仓库地址归一化成 `host[:port]/path`。
func NormalizeRepoURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > maxRepoURLLen || strings.ContainsFunc(value, isSpaceOrControl) {
		return "", fmt.Errorf("%w: empty, too long or contains whitespace", ErrInvalidRepoURL)
	}
	addr, err := splitRepoURL(value)
	if err != nil {
		return "", err
	}
	host := strings.ToLower(strings.TrimSuffix(addr.host, "."))
	if host == "" {
		return "", fmt.Errorf("%w: missing host", ErrInvalidRepoURL)
	}
	path, err := normalizeRepoPath(addr.path)
	if err != nil {
		return "", err
	}
	if caseInsensitiveHosts[host] {
		path = strings.ToLower(path)
	}
	if addr.port != "" {
		host = net.JoinHostPort(host, addr.port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]" // IPv6 字面量没有端口时也要带方括号，避免与 host:port 混淆
	}
	return host + "/" + path, nil
}

// repoAddress 是拆出来、尚未规范化的地址三段（port 已去掉该协议的默认端口）。
type repoAddress struct {
	host, port, path string
}

// splitRepoURL 拆出 host、端口与原始 path（带协议的走 URL 解析，否则按 scp 式）。
func splitRepoURL(value string) (repoAddress, error) {
	scheme, _, ok := strings.Cut(value, "://")
	if !ok {
		return splitSCP(value)
	}
	return splitSchemeURL(value, strings.ToLower(scheme))
}

// splitSchemeURL 解析带协议的地址（只接受 defaultPorts 里的协议）。
func splitSchemeURL(value, scheme string) (repoAddress, error) {
	defaultPort, supported := defaultPorts[scheme]
	if !supported {
		return repoAddress{}, fmt.Errorf("%w: unsupported scheme %q", ErrInvalidRepoURL, scheme)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return repoAddress{}, fmt.Errorf("%w: %w", ErrInvalidRepoURL, err)
	}
	port := parsed.Port()
	if n, cerr := strconv.Atoi(port); port != "" && (cerr != nil || n <= 0 || n > 65535) {
		return repoAddress{}, fmt.Errorf("%w: invalid port %q", ErrInvalidRepoURL, port)
	}
	if port == defaultPort {
		port = ""
	}
	return repoAddress{host: parsed.Hostname(), port: port, path: parsed.Path}, nil
}

// splitSCP 解析 scp 式 `[user@]host:path`（没有端口；`host:path` 的冒号后面是路径）。
func splitSCP(value string) (repoAddress, error) {
	// userinfo 只可能出现在主机之前（第一个 `:` / `[` 之前）；路径里的 `@` 不动。
	if at := strings.Index(value, "@"); at >= 0 {
		if c := strings.IndexAny(value, ":["); c < 0 || at < c {
			value = value[at+1:]
		}
	}
	if strings.HasPrefix(value, "[") { // IPv6：[::1]:org/repo
		end := strings.Index(value, "]:")
		if end < 0 {
			return repoAddress{}, fmt.Errorf("%w: malformed scp-style address", ErrInvalidRepoURL)
		}
		return repoAddress{host: value[1:end], path: value[end+2:]}, nil
	}
	colon := strings.Index(value, ":")
	// 没有冒号、冒号前出现 `/`（本地路径 ./a:b、/x/y）、或主机只有一个字符（Windows 盘符 C:/repo）：
	// 都不是 scp 式（与 git 自己的判定一致）。
	if colon <= 1 || strings.Contains(value[:colon], "/") {
		return repoAddress{}, fmt.Errorf("%w: not a URL or scp-style address", ErrInvalidRepoURL)
	}
	return repoAddress{host: value[:colon], path: value[colon+1:]}, nil
}

// normalizeRepoPath 去首尾 `/`、去末尾 `.git`、折叠重复 `/`；拒绝空路径与 `.` / `..` 段。
func normalizeRepoPath(path string) (string, error) {
	segments := make([]string, 0, 4)
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case "":
			continue
		case ".", "..":
			return "", fmt.Errorf("%w: path must not contain . or .. segments", ErrInvalidRepoURL)
		}
		segments = append(segments, segment)
	}
	if len(segments) > 0 {
		last := strings.TrimSuffix(segments[len(segments)-1], ".git")
		if last == "" {
			segments = segments[:len(segments)-1]
		} else {
			segments[len(segments)-1] = last
		}
	}
	if len(segments) == 0 {
		return "", fmt.Errorf("%w: missing repository path", ErrInvalidRepoURL)
	}
	return strings.Join(segments, "/"), nil
}

// NormalizePathPrefix 规范化 monorepo 子目录前缀：去首尾 `/` 与 `./`、折叠重复 `/`；拒绝 `..` 与反斜杠。
// 空串表示整个仓库。大小写敏感（目录名在多数文件系统上区分大小写）。
func NormalizePathPrefix(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if len(value) > maxRepoURLLen || strings.ContainsFunc(value, isSpaceOrControl) || strings.Contains(value, `\`) {
		return "", fmt.Errorf("%w: path_prefix is too long or contains whitespace / backslash", ErrInvalidPathPrefix)
	}
	segments := make([]string, 0, 4)
	for _, segment := range strings.Split(value, "/") {
		switch segment {
		case "", ".":
			continue
		case "..":
			return "", fmt.Errorf("%w: path_prefix must not contain dot-dot segments", ErrInvalidPathPrefix)
		}
		segments = append(segments, segment)
	}
	return strings.Join(segments, "/"), nil
}

// isSpaceOrControl 报告字符是否为空白或控制字符。
func isSpaceOrControl(r rune) bool {
	return r <= ' ' || r == 0x7f
}
