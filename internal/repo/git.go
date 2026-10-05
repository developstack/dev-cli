// Package repo 是 dev-cli 对本地 git 仓库的读写：定位仓库根、收集 remote、子路径、忽略规则（设计稿 §5.3、§4.5）。
//
// 只执行本地 git 命令（不联网）。子进程的环境去掉了 GIT_DIR / GIT_WORK_TREE 等"指定仓库位置"的变量：
// dev-cli 总是用 `git -C <dir>` 从目录发现仓库，继承来的 GIT_DIR（例如在 git 钩子里运行）会让命令打到
// 别的仓库上（scripts/without-git-env.sh 记录过真实事故）。
package repo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNotRepository 表示当前目录不在 git 仓库里（dev-cli 只服务登记过的项目仓库）。
var ErrNotRepository = errors.New("not inside a git repository: dev-cli only works in a registered project repository")

// locationVars 是会改变 git 找仓库方式的环境变量（子进程里去掉）。
var locationVars = map[string]bool{
	"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true, "GIT_PREFIX": true, "GIT_COMMON_DIR": true,
	"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true, "GIT_CEILING_DIRECTORIES": true,
}

// gitEnv 返回去掉仓库定位变量的环境。
func gitEnv() []string {
	env := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !locationVars[name] {
			env = append(env, kv)
		}
	}
	return env
}

// git 在 dir 里执行 git，返回去掉首尾空白的标准输出；退出码非 0 时返回 *exec.ExitError（附 stderr）。
func git(ctx context.Context, dir string, args ...string) (string, error) {
	//nolint:gosec // G204：程序名固定为 git，参数由本包组装（目录与相对路径），不经 shell。
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Location 是工作目录在仓库里的位置。
type Location struct {
	// Root 仓库根（工作树顶层）。
	Root string
	// Subpath 工作目录相对仓库根的子路径（正斜杠，空 = 仓库根；monorepo 匹配用）。
	Subpath string
}

// Locate 定位 dir 所在仓库的根与子路径；不在仓库里返回 ErrNotRepository。
func Locate(ctx context.Context, dir string) (Location, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return Location{}, errors.New("git is not installed or not on PATH")
	}
	root, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Location{}, ErrNotRepository
	}
	prefix, err := git(ctx, dir, "rev-parse", "--show-prefix")
	if err != nil {
		return Location{}, err
	}
	return Location{Root: filepath.FromSlash(root), Subpath: strings.TrimSuffix(prefix, "/")}, nil
}

// Remote 是一个 git remote（fetch 地址）。
type Remote struct {
	Name string
	URL  string
}

// Remotes 返回全部 remote 的 fetch 地址，顺序 origin、upstream、其它按名字（设计稿 §5.3：收集**全部** remote，
// fork 的 origin 不匹配时 upstream 仍能命中）。
func Remotes(ctx context.Context, root string) ([]Remote, error) {
	out, err := git(ctx, root, "remote", "-v")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var remotes []Remote
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[2] != "(fetch)" || seen[fields[0]] {
			continue
		}
		seen[fields[0]] = true
		remotes = append(remotes, Remote{Name: fields[0], URL: fields[1]})
	}
	sort.SliceStable(remotes, func(i, j int) bool { return remoteRank(remotes[i]) < remoteRank(remotes[j]) })
	return remotes, nil
}

// remoteRank 是排序键：origin < upstream < 其它（按名字）。
func remoteRank(r Remote) string {
	switch r.Name {
	case "origin":
		return "0"
	case "upstream":
		return "1"
	default:
		return "2" + r.Name
	}
}

// Fingerprint 是 remotes + 子路径的指纹（与 .aidevstack/project.json 里记的相同时 start 跳过 resolve，设计稿 §5.3）。
// 用归一化地址，换写法（ssh ↔ https）不算变化；归一化失败的原样参与。
func Fingerprint(remotes []Remote, subpath string) string {
	parts := make([]string, 0, len(remotes)+1)
	for _, r := range remotes {
		url, err := NormalizeRepoURL(r.URL)
		if err != nil {
			url = r.URL
		}
		parts = append(parts, r.Name+"="+url)
	}
	sort.Strings(parts)
	return strings.Join(append(parts, "subpath="+subpath), "\n")
}
