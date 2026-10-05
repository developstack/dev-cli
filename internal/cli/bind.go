package cli

// 仓库 → 项目绑定（设计稿 §5.3、§10.2 第 3 步）：remotes 指纹与 .aidevstack/project.json 相同则跳过 resolve，
// 否则上报全部 remote 与子路径；多个候选时交互选择或用 --project 指定，选择写回 project.json。

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/developstack/aidevstack/cli/internal/api"
	"github.com/developstack/aidevstack/cli/internal/repo"
	"github.com/developstack/aidevstack/cli/internal/workspace"
)

// bindProject 返回本仓库绑定的项目（必要时 resolve 并写回 project.json）。
func (a *App) bindProject(
	ctx context.Context, s *session, ws workspace.Workspace, loc repo.Location, opts syncOptions,
) (workspace.Project, error) {
	override := opts.project
	remotes, err := repo.Remotes(ctx, loc.Root)
	if err != nil {
		return workspace.Project{}, err
	}
	if len(remotes) == 0 {
		return workspace.Project{}, fmt.Errorf("this repository has no git remotes; add the project's remote first " +
			"(git remote add origin <url>)")
	}
	fingerprint := repo.Fingerprint(remotes, loc.Subpath)
	current, found, err := ws.LoadProject()
	if err != nil {
		return workspace.Project{}, err
	}
	if found && reusable(current, s.cred.Platform, fingerprint, override) {
		return current, nil
	}
	hint := override
	if hint == "" {
		hint = current.ProjectID
	}
	result, err := a.resolve(ctx, s, opts.flags, resolveRequest(remotes, loc.Subpath, hint))
	if api.IsCode(err, api.DevenvErrorCodeProjectAmbiguous) && override == "" && a.Interactive {
		choice, chooseErr := a.chooseProject(err)
		if chooseErr != nil {
			return workspace.Project{}, chooseErr
		}
		opts.project = choice
		return a.bindProject(ctx, s, ws, loc, opts)
	}
	if err != nil {
		return workspace.Project{}, err
	}
	if override != "" && result.Project.ID != override {
		_, _ = fmt.Fprintf(a.Err, "warning: --project %s does not match this repository; it belongs to %s (%s)\n",
			override, result.Project.Name, result.Project.ID)
	}
	binding := workspace.Project{
		Platform: s.cred.Platform, ProjectID: result.Project.ID, ProjectName: result.Project.Name,
		TeamName: result.Project.TeamName, Remote: result.Matched.Remote, URL: result.Matched.URLNormalized,
		PathPrefix: result.Matched.PathPrefix, Fingerprint: fingerprint, BoundAt: a.Now().UTC().Truncate(time.Second),
	}
	return binding, ws.SaveProject(binding)
}

// reusable 报告上次的绑定能否直接用：同一平台、remotes 与子路径没变、没有用 --project 改选别的项目。
// 跳过 resolve 不跳过授权：清单接口每次都校验成员资格（设计稿 §5.3）。
func reusable(current workspace.Project, platform, fingerprint, override string) bool {
	return current.Platform == platform && current.Fingerprint == fingerprint &&
		(override == "" || override == current.ProjectID)
}

// resolve 调 /devenv/resolve（401 时重新登录一次）。
func (a *App) resolve(
	ctx context.Context, s *session, flags commonFlags, req api.DevenvResolveRequest,
) (api.DevenvResolveResult, error) {
	var result api.DevenvResolveResult
	err := a.withRelogin(ctx, s, flags, func(s *session) error {
		var err error
		result, err = s.client.Resolve(ctx, req)
		return err
	})
	return result, err
}

// resolveRequest 组装 resolve 请求体（生成的 DTO 里 remotes 是匿名结构体）。
func resolveRequest(remotes []repo.Remote, subpath, hint string) api.DevenvResolveRequest {
	req := api.DevenvResolveRequest{Subpath: &subpath}
	if hint != "" {
		req.ProjectHint = &hint
	}
	for _, r := range remotes {
		name := r.Name
		req.Remotes = append(req.Remotes, struct {
			Name *string `json:"name,omitempty"`
			URL  string  `json:"url"`
		}{Name: &name, URL: r.URL})
	}
	return req
}

// chooseProject 在终端里让用户从候选项目中选一个。
func (a *App) chooseProject(ambiguous error) (string, error) {
	candidates := api.ResolveDetailsOf(ambiguous).Candidates
	if len(candidates) == 0 {
		return "", ambiguous
	}
	_, _ = fmt.Fprintln(a.Err, "This repository matches several of your projects:")
	for i, c := range candidates {
		_, _ = fmt.Fprintf(a.Err, "  [%d] %s  %s  (via %s %s)\n",
			i+1, c.ProjectID, c.ProjectName, c.Remote, c.URLNormalized)
	}
	_, _ = fmt.Fprintf(a.Err, "Choose a project [1-%d]: ", len(candidates))
	line, err := bufio.NewReader(a.In).ReadString('\n')
	if err != nil {
		return "", ambiguous
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(candidates) {
		return "", usageError{msg: "invalid choice; rerun with --project <id>"}
	}
	return candidates[n-1].ProjectID, nil
}
