package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/developstack/aidevstack/cli/internal/api"
)

// explain 把错误翻成给人看的一句话（平台错误按 error_code 给出下一步指引，不复述服务端文案）。
func explain(err error) string {
	var upgrade *api.UpgradeError
	if errors.As(err, &upgrade) {
		return upgrade.Error()
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		return err.Error()
	}
	if hint := hintFor(apiErr); hint != "" {
		return hint + "\n  (" + apiErr.Error() + ")"
	}
	return apiErr.Error()
}

// hintFor 是按稳定错误码给出的指引。
func hintFor(e *api.Error) string {
	switch e.ErrorCode { //nolint:exhaustive // 只有需要给出下一步指引的错误码；其余直接显示平台的说明
	case api.DevenvErrorCodeProjectNotRegistered:
		urls := api.ResolveDetailsOf(e).NormalizedURLs
		return "this repository is not registered to any project on the platform.\n" +
			"  Ask your group admin to register it (project → repositories): " + strings.Join(urls, ", ")
	case api.DevenvErrorCodeNotProjectMember:
		return "this repository belongs to a project you are not a member of:\n" + describeCandidates(e) +
			"  Ask the project's group admin to add you to its team."
	case api.DevenvErrorCodeProjectAmbiguous:
		return "this repository matches several projects; choose one with `--project <id>`:\n" + describeCandidates(e)
	case api.DevenvErrorCodeProjectDisabled:
		return "this project is disabled on the platform; contact its group admin."
	case api.DevenvErrorCodeDevenvDisabled:
		return "dev-cli is not enabled for this project; a group admin can enable it in the project's settings."
	case api.DevenvErrorCodeDeveloperKeyDisabled:
		return "your developer key for this project was disabled by an administrator."
	case api.DevenvErrorCodeModelsUnavailable:
		return "the platform's model catalog is temporarily unavailable; retry in a moment."
	default:
		return ""
	}
}

// describeCandidates 列出候选项目（一行一个）。
func describeCandidates(e *api.Error) string {
	var b strings.Builder
	for _, c := range api.ResolveDetailsOf(e).Candidates {
		line := fmt.Sprintf("    %s  %s", c.ProjectID, c.ProjectName)
		if c.TeamName != "" {
			line += "  (team " + c.TeamName + ")"
		}
		if c.Manager != "" {
			line += "  owner: " + c.Manager
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
