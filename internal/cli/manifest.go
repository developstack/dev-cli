package cli

// 清单（设计稿 §10.2 第 5 步）：带上次的 ETag 拉取，304 用本地缓存；平台不可达且 --use-cache 时用 24 小时内的缓存。

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/developstack/aidevstack/cli/internal/api"
	"github.com/developstack/aidevstack/cli/internal/fsutil"
	"github.com/developstack/aidevstack/cli/internal/workspace"
)

// maxCacheAge 是 --use-cache 能接受的缓存年龄。
const maxCacheAge = 24 * time.Hour

// manifestCache 是清单的本地缓存（用户级状态目录，无秘密）。
type manifestCache struct {
	ETag      string             `json:"etag"`
	FetchedAt time.Time          `json:"fetched_at"`
	Manifest  api.DevenvManifest `json:"manifest"`
}

// loadManifest 拉清单（或用缓存），校验大版本与最低 dev-cli 版本。
func (a *App) loadManifest(
	ctx context.Context, s *session, projectID string, opts syncOptions,
) (api.DevenvManifest, error) {
	dirs, err := a.dirs()
	if err != nil {
		return api.DevenvManifest{}, err
	}
	path := dirs.ManifestCache(projectID)
	var cache manifestCache
	cached, err := fsutil.ReadJSON(path, &cache)
	if err != nil {
		cached = false // 缓存损坏就当没有：重新拉一份
	}
	etag := ""
	if cached {
		etag = cache.ETag
	}
	var result api.ManifestResult
	err = a.withRelogin(ctx, s, opts.flags, func(s *session) error {
		result, err = s.client.Manifest(ctx, projectID, etag)
		return err
	})
	switch {
	case err == nil && result.NotModified && cached:
		cache.FetchedAt = a.Now().UTC()
	case err == nil:
		cache = manifestCache{ETag: result.ETag, FetchedAt: a.Now().UTC(), Manifest: result.Manifest}
	case opts.useCache && cached && isNetworkError(err) && a.Now().Sub(cache.FetchedAt) < maxCacheAge:
		_, _ = fmt.Fprintf(a.Err, "warning: platform unreachable (%v); using the manifest cached at %s\n",
			err, cache.FetchedAt.Local().Format(time.DateTime))
		return cache.Manifest, checkManifest(cache.Manifest)
	default:
		return api.DevenvManifest{}, err
	}
	if err := checkManifest(cache.Manifest); err != nil {
		return api.DevenvManifest{}, err
	}
	if err := fsutil.EnsureDir(filepath.Dir(path), 0o700); err != nil {
		return api.DevenvManifest{}, err
	}
	return cache.Manifest, fsutil.WriteJSONAtomic(path, cache, 0o600)
}

// isNetworkError 报告错误是否是"连不上平台"（而不是平台给出的拒绝）。
func isNetworkError(err error) bool {
	var apiErr *api.Error
	var upgrade *api.UpgradeError
	return err != nil && !errors.As(err, &apiErr) && !errors.As(err, &upgrade)
}

// workspaceIndexAdd 记下仓库根（logout --all 用）。
func workspaceIndexAdd(indexPath, root string) error {
	return workspace.NewIndex(indexPath).Add(root)
}
