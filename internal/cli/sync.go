package cli

// 同步流水线（设计稿 §10.2 第 0–8 步）：定位仓库 → 仓库锁 → CLI 凭据 → 绑定项目 → 忽略规则 → 开发者密钥 →
// 清单 → 渲染托管目录与安装技能 → 释放锁。start 在此之后组装环境并 exec；sync 到此为止。

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/developstack/aidevstack/cli/internal/adapter"
	"github.com/developstack/aidevstack/cli/internal/api"
	"github.com/developstack/aidevstack/cli/internal/buildinfo"
	"github.com/developstack/aidevstack/cli/internal/managed"
	"github.com/developstack/aidevstack/cli/internal/repo"
	"github.com/developstack/aidevstack/cli/internal/secret"
	"github.com/developstack/aidevstack/cli/internal/skills"
	"github.com/developstack/aidevstack/cli/internal/workspace"
)

// manifestMajor 是 dev-cli 认识的清单大版本。
const manifestMajor = 1

// syncOptions 是一次同步的参数。
type syncOptions struct {
	flags commonFlags
	// project --project 指定的项目 id（歧义时消歧，或覆盖上次的绑定）。
	project string
	// agents 要渲染的 agent（空 = 只同步凭据与清单）。
	agents []adapter.Adapter
	// useCache 平台不可达时用 24 小时内的缓存清单（设计稿 §10.3）。
	useCache bool
}

// syncOutcome 是同步的结果（start 用它组装启动参数）。
type syncOutcome struct {
	loc      repo.Location
	binding  workspace.Project
	creds    workspace.Credentials
	manifest api.DevenvManifest
}

// syncProject 执行同步流水线。仓库锁只覆盖"同步与写入"，返回前释放（多个 agent 会话可以并行）。
func (a *App) syncProject(ctx context.Context, opts syncOptions) (syncOutcome, error) {
	var out syncOutcome
	cwd, err := a.Getwd()
	if err != nil {
		return out, err
	}
	if out.loc, err = repo.Locate(ctx, cwd); err != nil {
		return out, err
	}
	ws := workspace.Open(out.loc.Root)
	lock, err := ws.Lock(ctx)
	if err != nil {
		return out, err
	}
	defer lock.Unlock()

	s, err := a.signedIn(ctx, opts.flags)
	if err != nil {
		return out, err
	}
	if out.binding, err = a.bindProject(ctx, s, ws, out.loc, opts); err != nil {
		return out, err
	}
	if err := a.ensureIgnored(ctx, out.loc.Root); err != nil {
		return out, err
	}
	if out.creds, err = a.ensureDeveloperKey(ctx, s, ws, out.binding, opts.flags); err != nil {
		return out, err
	}
	if out.manifest, err = a.loadManifest(ctx, s, out.binding.ProjectID, opts); err != nil {
		return out, err
	}
	dirs, err := a.dirs()
	if err != nil {
		return out, err
	}
	if err := workspaceIndexAdd(dirs.WorkspaceIndex(), out.loc.Root); err != nil {
		return out, err
	}
	for _, agent := range opts.agents {
		if err := a.renderAgent(ctx, s.client, agent, out.binding.ProjectID, out.manifest); err != nil {
			return out, err
		}
	}
	return out, nil
}

// ensureIgnored 保证 .aidevstack/ 被 git 忽略（写凭证之前；做不到就拒绝继续，设计稿 §4.5）。
func (a *App) ensureIgnored(ctx context.Context, root string) error {
	changed, err := repo.EnsureIgnored(ctx, root, workspace.CredentialsRelPath())
	if err != nil {
		return fmt.Errorf("refusing to write credentials into the repository: %w", err)
	}
	if changed {
		_, _ = fmt.Fprintln(a.Err,
			"Added .aidevstack/ to .gitignore (and .git/info/exclude); commit the .gitignore change.")
	}
	return nil
}

// ensureDeveloperKey 每次都向平台确认本地凭证（Q5 静默续签）：仍有效且不在续签窗口内时平台回 valid、不签发；
// 已吊销（设备被吊销、管理员停用了开发者密钥）、过期或进入续签窗口时签发新凭证，原子写入 0600。
//
// 为什么不按本地到期时间跳过这次往返：本地的有效期判断只是体验优化（设计稿 §4.3），它看不到服务端的吊销 ——
// 跳过时 agent 会带着已吊销的凭证启动、跑到第一次推理才 401；而管理员停用的密钥要等本地进入续签窗口才会
// 得到 developer_key_disabled 的指引。连不上平台时退回本地判断：凭证仍新鲜就照用（离线降级，清单按 --use-cache 决定）。
func (a *App) ensureDeveloperKey(
	ctx context.Context, s *session, ws workspace.Workspace, binding workspace.Project, flags commonFlags,
) (workspace.Credentials, error) {
	creds, _, err := ws.LoadCredentials()
	if err != nil {
		return workspace.Credentials{}, err
	}
	have := ""
	if creds.ProjectID == binding.ProjectID && creds.Platform == s.cred.Platform {
		have = creds.CredentialID
	}
	var key api.DevenvDeveloperKey
	err = a.withRelogin(ctx, s, flags, func(s *session) error {
		key, err = s.client.EnsureDeveloperKey(ctx, binding.ProjectID, have)
		return err
	})
	if err != nil {
		if isNetworkError(err) && !creds.NeedsRenewal(s.cred.Platform, binding.ProjectID, a.Now()) {
			return creds, nil
		}
		return workspace.Credentials{}, err
	}
	creds.RenewBefore = time.Duration(key.RenewBeforeSeconds) * time.Second
	creds.ExpiresAt = key.Credential.ExpiresAt
	if key.Status == api.Issued {
		if key.Credential.Secret == nil {
			return workspace.Credentials{}, errors.New("the platform issued a developer key without its secret")
		}
		creds = workspace.Credentials{
			Platform: s.cred.Platform, ProjectID: binding.ProjectID, VirtualKeyID: key.VirtualKeyID,
			CredentialID: key.Credential.ID, Prefix: key.Credential.Prefix, Secret: secret.New(*key.Credential.Secret),
			ExpiresAt: key.Credential.ExpiresAt, RenewBefore: creds.RenewBefore,
		}
	}
	if err := ws.SaveCredentials(creds); err != nil {
		return workspace.Credentials{}, err
	}
	return creds, nil
}

// renderAgent 把清单渲染进该 agent 的托管目录（配置文件 + 技能），并打印篡改 / 删除自带凭据的告警。
func (a *App) renderAgent(
	ctx context.Context, dl skills.Downloader, agent adapter.Adapter, projectID string, m api.DevenvManifest,
) error {
	dirs, err := a.dirs()
	if err != nil {
		return err
	}
	dir := dirs.AgentDir(projectID, agent.Name())
	files, err := agent.Files(adapter.RenderInput{Manifest: m, AgentDir: dir, CLIVersion: buildinfo.CurrentVersion()})
	if err != nil {
		return err
	}
	result, err := managed.Sync(dir, managedStatePath(dirs.State, projectID, agent.Name()), files, agent.Forbidden())
	if err != nil {
		return err
	}
	for _, name := range result.Tampered {
		_, _ = fmt.Fprintf(a.Err,
			"warning: managed file %s was modified outside dev-cli; restored the platform version\n",
			filepath.Join(dir, name))
	}
	for _, name := range result.Forbidden {
		_, _ = fmt.Fprintf(a.Err, "warning: removed %s from the managed %s directory "+
			"(bring-your-own credentials are not supported under platform management)\n", name, agent.Name())
	}
	// 技能：按 digest 取包（缓存命中不下载）、sha256 与清单一致才写入；清单里没有的（吊销 / 停用）删除本地副本。
	installed, err := skills.Sync(ctx, dl, dirs.SkillCache(), filepath.Join(dir, "skills"), m.Skills)
	if err != nil {
		return err
	}
	for _, name := range installed.Installed {
		_, _ = fmt.Fprintf(a.Err, "Installed skill %s for %s.\n", name, agent.Name())
	}
	for _, name := range installed.Removed {
		_, _ = fmt.Fprintf(a.Err, "Removed skill %s from %s (no longer published for this project).\n",
			name, agent.Name())
	}
	return nil
}

// managedStatePath 是托管文件的 sha256 记录（与托管目录同级：同一项目的多个仓库共用一个托管目录）。
func managedStatePath(state, projectID, agent string) string {
	return filepath.Join(state, "agents", projectID, agent+".managed.json")
}

// checkManifest 拒绝不认识的清单大版本，以及要求更高 dev-cli 版本的清单。
func checkManifest(m api.DevenvManifest) error {
	if int(m.SchemaVersion) != manifestMajor {
		return fmt.Errorf("the platform sent manifest schema v%d but this dev-cli understands v%d; upgrade dev-cli",
			m.SchemaVersion, manifestMajor)
	}
	if adapter.VersionBelow(buildinfo.CurrentVersion(), m.Policy.MinCliVersion) {
		return fmt.Errorf("this project requires dev-cli %s or later (you have %s); upgrade dev-cli",
			m.Policy.MinCliVersion, buildinfo.CurrentVersion())
	}
	return nil
}
