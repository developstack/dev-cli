package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/developstack/dev-cli/internal/platform"
	"github.com/developstack/dev-cli/internal/skills"
)

// runSync 实现 `dev-cli sync`：与平台对账，把差异落地。
func runSync(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	common := parseCommon(fs, os.Getenv)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	root, err := projectRoot(common.dir)
	if err != nil {
		return fail(stderr, err)
	}
	settings, err := requireAuth(root)
	if err != nil {
		return fail(stderr, err)
	}

	endpoint := common.endpoint
	if endpoint == "" {
		endpoint = settings.Endpoint
	}
	client := platform.New(endpoint, settings.APIKey)

	fmt.Fprintf(stdout, "与平台对账：%s（项目 %s）\n", client.Endpoint(), settings.ProjectName)
	resp, err := client.Sync(ctx, platform.SyncRequest{
		AgentID: settings.AgentID, AgentType: "dev-cli",
		ProjectID: settings.ProjectID, Scope: "project",
		InstalledSkills: installedAsReport(root),
	})
	if err != nil {
		return fail(stderr, err)
	}
	if len(resp.Commands) == 0 {
		fmt.Fprintln(stdout, "✅ 已是最新，无需变更")
		return 0
	}

	applied, failed := 0, 0
	for _, cmd := range resp.Commands {
		if err := applyCommand(ctx, client, root, cmd, stdout); err != nil {
			failed++
			fmt.Fprintf(stderr, "  ✗ %s %s：%v\n", cmd.Type, cmd.SkillSlug, err)
			_ = client.Ack(ctx, cmd.ID, "failed", err.Error())
			continue
		}
		applied++
		_ = client.Ack(ctx, cmd.ID, "success", "")
	}
	fmt.Fprintf(stdout, "\n完成：成功 %d 条，失败 %d 条\n", applied, failed)
	if failed > 0 {
		return 1
	}
	return 0
}

// applyCommand 执行一条平台指令。
func applyCommand(
	ctx context.Context, client *platform.Client, root string, cmd platform.Command, stdout io.Writer,
) error {
	switch cmd.Type {
	case platform.CommandInstallSkill:
		pkg, err := client.DownloadPackage(ctx, cmd.DownloadURL)
		if err != nil {
			return err
		}
		if err := skills.Install(root, cmd.SkillSlug, pkg); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "  ✓ 安装 %s\n", cmd.SkillSlug)
		return nil
	case platform.CommandUninstallSkill:
		if err := skills.Uninstall(root, cmd.SkillSlug); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "  ✓ 卸载 %s\n", cmd.SkillSlug)
		return nil
	default:
		return fmt.Errorf("未知指令类型 %q（可能需要升级 dev-cli）", cmd.Type)
	}
}

// installedAsReport 把本地已装技能转成上报形状。
//
// 学习点：只报"平台下发的那些"（本地真源里的），**不扫描工具目录** ——
// 用户自己放进 `.claude/skills` 的技能不该被算作"平台装的"，
// 否则平台会按自己的有效集把它们卸载掉。
func installedAsReport(root string) []platform.InstalledSkill {
	slugs := skills.Installed(root)
	out := make([]platform.InstalledSkill, 0, len(slugs))
	for _, slug := range slugs {
		out = append(out, platform.InstalledSkill{Slug: slug})
	}
	return out
}

// runStatus 实现 `dev-cli status`。
func runStatus(_ context.Context, args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	common := parseCommon(fs, os.Getenv)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root, err := projectRoot(common.dir)
	if err != nil {
		fmt.Fprintf(stdout, "✗ %s\n", err)
		return 1
	}
	settings, err := requireAuth(root)
	if err != nil {
		fmt.Fprintf(stdout, "%s\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "状态：已认证\n")
	fmt.Fprintf(stdout, "  平台    ：%s\n", settings.Endpoint)
	fmt.Fprintf(stdout, "  项目    ：%s [id=%s]\n", settings.ProjectName, settings.ProjectID)
	fmt.Fprintf(stdout, "  本机标识：%s\n", settings.AgentID)

	installed := skills.Installed(root)
	if len(installed) == 0 {
		fmt.Fprintln(stdout, "  已装技能：（无）")
		return 0
	}
	fmt.Fprintf(stdout, "  已装技能：%s\n", join(installed))
	return 0
}
