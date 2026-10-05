package api_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/developstack/aidevstack/cli/internal/api"
	"github.com/developstack/aidevstack/cli/internal/buildinfo"
	"github.com/developstack/aidevstack/cli/internal/secret"
	"github.com/developstack/aidevstack/cli/test/fakeplatform"
)

// TestClientHeadersAndErrors 每个请求带 dev-cli UA 与版本头；带凭据时 Bearer；401 / error_code 解析成 *api.Error。
func TestClientHeadersAndErrors(t *testing.T) {
	p := fakeplatform.New(t)
	p.Tokens["adsk_cli_good"] = "cli_1"
	ctx := context.Background()
	client := api.New(p.URL())

	if _, err := client.ListDevices(ctx); !api.IsUnauthorized(err) {
		t.Fatalf("没有凭据应 401：%v", err)
	}
	devices, err := client.WithToken(secret.New("adsk_cli_good")).ListDevices(ctx)
	if err != nil || len(devices) != 1 || !devices[0].Current {
		t.Fatalf("ListDevices = %+v %v", devices, err)
	}
	p.Snapshot(func(p *fakeplatform.Platform) {
		last := p.Requests[len(p.Requests)-1]
		if !strings.HasPrefix(last.UserAgent, "dev-cli/") || last.Auth != "Bearer adsk_cli_good" {
			t.Fatalf("请求头 = %+v", last)
		}
	})

	_, err = client.WithToken(secret.New("adsk_cli_good")).Resolve(ctx, api.DevenvResolveRequest{
		Remotes: []struct {
			Name *string `json:"name,omitempty"`
			URL  string  `json:"url"`
		}{{URL: "git@github.com:acme/unknown.git"}},
	})
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Status != 404 || !api.IsCode(err, api.DevenvErrorCodeProjectNotRegistered) {
		t.Fatalf("未登记 = %v", err)
	}
	if urls := api.ResolveDetailsOf(err).NormalizedURLs; len(urls) != 1 || urls[0] != "github.com/acme/unknown" {
		t.Fatalf("details = %+v", urls)
	}
}

// TestManifestNotModified 带上次的 ETag → 304 NotModified；ETag 不同 → 200 新清单。
func TestManifestNotModified(t *testing.T) {
	p := fakeplatform.New(t)
	p.Tokens["tok"] = "cli_1"
	p.Manifest["p1"] = api.DevenvManifest{SchemaVersion: 1, Project: api.DevenvProject{ID: "p1", Name: "Payments"}}
	client := api.New(p.URL()).WithToken(secret.New("tok"))
	ctx := context.Background()
	first, err := client.Manifest(ctx, "p1", "")
	if err != nil || first.NotModified || first.ETag == "" || first.Manifest.Project.Name != "Payments" {
		t.Fatalf("first = %+v %v", first, err)
	}
	again, err := client.Manifest(ctx, "p1", first.ETag)
	if err != nil || !again.NotModified || again.ETag != first.ETag {
		t.Fatalf("304 = %+v %v", again, err)
	}
	if _, err := client.Manifest(ctx, "nope", ""); !api.IsCode(err, api.DevenvErrorCodeProjectNotFound) {
		t.Fatalf("不存在的项目 = %v", err)
	}
}

// TestUpgradeRequired 版本低于平台最低版本 → 426 解析成 *api.UpgradeError，文案带最低版本与升级命令。
func TestUpgradeRequired(t *testing.T) {
	p := fakeplatform.New(t)
	p.MinVersion = "2.0.0"
	old := buildinfo.Version
	buildinfo.Version = "1.9.0"
	t.Cleanup(func() { buildinfo.Version = old })
	_, err := api.New(p.URL()).RequestDevice(context.Background(), api.DeviceAuthorizationRequest{ClientID: "dev-cli"})
	var upgrade *api.UpgradeError
	if !errors.As(err, &upgrade) || upgrade.Details.MinVersion != "2.0.0" || upgrade.Details.CurrentVersion != "1.9.0" ||
		!strings.Contains(err.Error(), "brew upgrade developstack/tap/dev-cli") {
		t.Fatalf("426 = %v", err)
	}
}
