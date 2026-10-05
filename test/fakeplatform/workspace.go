package fakeplatform

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/developstack/aidevstack/cli/internal/api"
)

// resolve 按地址精确匹配（服务端的归一化与最长前缀由后端测试覆盖；这里只认已归一化的 https 形状）。
func (p *Platform) resolve(w http.ResponseWriter, r *http.Request) {
	var req api.DevenvResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Remotes) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "bad body", nil)
		return
	}
	urls := []string{}
	for _, remote := range req.Remotes {
		normalized := normalizeForFake(remote.URL)
		urls = append(urls, normalized)
		if project, ok := p.Remotes[normalized]; ok {
			result := api.DevenvResolveResult{Project: project}
			result.Matched.URLNormalized, result.Matched.Remote = normalized, deref(remote.Name)
			writeData(w, result)
			return
		}
	}
	writeError(w, http.StatusNotFound, "project_not_registered", "not registered",
		map[string]any{"candidates": []any{}, "normalized_urls": urls})
}

// normalizeForFake 是假平台的极简归一化（git@host:org/repo.git 与 https://host/org/repo 归一为 host/org/repo）。
func normalizeForFake(raw string) string {
	s := strings.TrimSuffix(strings.TrimSpace(raw), ".git")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "ssh://")
	if at := strings.Index(s, "@"); at >= 0 {
		s = s[at+1:]
	}
	return strings.ToLower(strings.Replace(s, ":", "/", 1))
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// developerKey：have 仍在签发表里且剩余 > 2 天 → valid；否则签发新凭证。
func (p *Platform) developerKey(w http.ResponseWriter, r *http.Request, device string) {
	var req api.DevenvDeveloperKeyRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	if p.DeveloperKeyDisabled {
		writeError(w, http.StatusForbidden, string(api.DevenvErrorCodeDeveloperKeyDisabled),
			"developer key is disabled", nil)
		return
	}
	out := api.DevenvDeveloperKey{VirtualKeyID: "vk-" + device, RenewBeforeSeconds: 2 * 24 * 3600}
	if have := deref(req.HaveCredentialID); have != "" {
		if expires, ok := p.Issued[have]; ok && time.Until(expires) > 48*time.Hour {
			out.Status = api.Valid
			out.Credential.ID, out.Credential.Prefix, out.Credential.ExpiresAt = have, "adsk_dev_"+have[:4], expires
			writeData(w, out)
			return
		}
	}
	p.seq++
	id := fmt.Sprintf("vkc%04d", p.seq)
	secret := "adsk_dev_secret_" + id
	expires := time.Now().UTC().Add(p.KeyTTL).Truncate(time.Second)
	p.Issued[id] = expires
	out.Status = api.Issued
	out.Credential.ID, out.Credential.Prefix, out.Credential.ExpiresAt = id, "adsk_dev_sec", expires
	out.Credential.Secret = &secret
	writeData(w, out)
}

// manifest：ETag = 清单 JSON 的 sha256，If-None-Match 命中 304。
func (p *Platform) manifest(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/devenv/projects/"), "/manifest")
	m, ok := p.Manifest[id]
	if !ok {
		writeError(w, http.StatusNotFound, "project_not_found", "project not found", nil)
		return
	}
	raw, _ := json.Marshal(m)
	sum := sha256.Sum256(raw)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeData(w, m)
}
