package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/developstack/aidevstack/cli/internal/fsutil"
	"github.com/developstack/aidevstack/cli/internal/managed"
	"github.com/developstack/aidevstack/cli/internal/repo"
	"github.com/developstack/aidevstack/cli/internal/workspace"
)

// runStatus 处理 `dev-cli status`：只读本地状态（不联网），给出绑定、凭证到期、清单版本、托管文件完整性与 agent 版本。
func runStatus(ctx context.Context, a *App, args []string) error {
	fs := a.newFlagSet("status", "status")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	cwd, err := a.Getwd()
	if err != nil {
		return err
	}
	loc, err := repo.Locate(ctx, cwd)
	if err != nil {
		return err
	}
	ws := workspace.Open(loc.Root)
	binding, found, err := ws.LoadProject()
	if err != nil {
		return err
	}
	if !found {
		_, _ = fmt.Fprintf(a.Out, "repository: %s\nproject:    not bound (run `dev-cli init` or `dev-cli start`)\n",
			loc.Root)
		return nil
	}
	_, _ = fmt.Fprintf(a.Out, "repository: %s\nplatform:   %s\nproject:    %s (%s) via %s %s\n", loc.Root,
		binding.Platform, binding.ProjectName, binding.ProjectID, binding.Remote, binding.URL)
	a.printCredentialStatus(ws, binding)
	return a.printAgentStatus(ctx, binding.ProjectID)
}

// printCredentialStatus 打印开发者密钥凭证的状态（只显示前缀，从不显示明文）。
func (a *App) printCredentialStatus(ws workspace.Workspace, binding workspace.Project) {
	creds, found, err := ws.LoadCredentials()
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(a.Out, "key:        unreadable (%v)\n", err)
	case !found:
		_, _ = fmt.Fprintln(a.Out, "key:        none yet (issued on the next start / sync)")
	default:
		state := "valid"
		if creds.NeedsRenewal(binding.Platform, binding.ProjectID, a.Now()) {
			state = "renews on the next start / sync"
		}
		_, _ = fmt.Fprintf(a.Out, "key:        %s… expires %s (%s)\n", creds.Prefix,
			creds.ExpiresAt.Local().Format(time.DateTime), state)
	}
}

// printAgentStatus 打印清单缓存与各 agent 托管目录的完整性、已安装版本。
func (a *App) printAgentStatus(ctx context.Context, projectID string) error {
	dirs, err := a.dirs()
	if err != nil {
		return err
	}
	var cache manifestCache
	if found, err := fsutil.ReadJSON(dirs.ManifestCache(projectID), &cache); err == nil && found {
		_, _ = fmt.Fprintf(a.Out, "manifest:   %s fetched %s, %d models, default %s\n", shortETag(cache.ETag),
			cache.FetchedAt.Local().Format(time.DateTime), len(cache.Manifest.Models), cache.Manifest.Defaults.Model)
	}
	for _, name := range []string{"pi", "claude"} {
		agent, ok := adapters()[name]
		if !ok {
			continue
		}
		tampered, err := managed.Verify(dirs.AgentDir(projectID, name), managedStatePath(dirs.State, projectID, name))
		if err != nil {
			return err
		}
		integrity := "intact"
		if len(tampered) > 0 {
			integrity = "modified: " + strings.Join(tampered, ", ") + " (restored on the next start)"
		}
		_, installed, _ := a.checkAgent(ctx, agent)
		if installed == "" {
			installed = "not installed"
		}
		_, _ = fmt.Fprintf(a.Out, "%-11s %s; managed config %s\n", name+":", installed, integrity)
	}
	return nil
}

// shortETag 截短 ETag 便于阅读。
func shortETag(etag string) string {
	etag = strings.Trim(etag, `"`)
	if len(etag) > 12 {
		return etag[:12]
	}
	return etag
}
