package skills_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/developstack/aidevstack/cli/internal/api"
	"github.com/developstack/aidevstack/cli/internal/skills"
)

// bundle 构造一个技能包（条目名原样，用来造越界条目）。
func bundle(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte(content))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// fakeDownloader 按 bundle_url 返回字节，记下下载次数。
type fakeDownloader struct {
	bundles map[string][]byte
	calls   int
}

func (f *fakeDownloader) DownloadSkill(_ context.Context, url string) ([]byte, error) {
	f.calls++
	data, ok := f.bundles[url]
	if !ok {
		return nil, errors.New("404")
	}
	return data, nil
}

func skill(name, version string, data []byte) api.DevenvSkill {
	return api.DevenvSkill{
		Name: name, Version: version, Digest: digest(data), Size: int64(len(data)),
		BundleURL: "/b/" + name + "/" + version + ".zip",
	}
}

// TestSyncInstallsCachesAndPrunes 安装（剥外层目录）→ 再次同步不下载 → 新版本替换 → 从清单移除即删除本地副本。
func TestSyncInstallsCachesAndPrunes(t *testing.T) {
	root := t.TempDir()
	cache, dir := filepath.Join(root, "cache"), filepath.Join(root, "agent", "skills")
	v1 := bundle(t, map[string]string{"review/SKILL.md": "v1", "review/scripts/run.sh": "echo"})
	other := bundle(t, map[string]string{"lint/SKILL.md": "lint"})
	dl := &fakeDownloader{bundles: map[string][]byte{"/b/review/1.0.0.zip": v1, "/b/lint/1.0.0.zip": other}}
	want := []api.DevenvSkill{skill("review", "1.0.0", v1), skill("lint", "1.0.0", other)}

	res, err := skills.Sync(context.Background(), dl, cache, dir, want)
	if err != nil || len(res.Installed) != 2 {
		t.Fatalf("Sync = %+v %v", res, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "review", "scripts", "run.sh")); string(got) != "echo" {
		t.Fatalf("run.sh = %q", got)
	}
	// 用户在托管目录里放了别的技能：同步时删除（托管目录不允许混入）。
	if err := os.MkdirAll(filepath.Join(dir, "my-own"), 0o700); err != nil {
		t.Fatal(err)
	}
	if res, err = skills.Sync(context.Background(), dl, cache, dir, want); err != nil || dl.calls != 2 ||
		len(res.Installed) != 0 || len(res.Removed) != 1 {
		t.Fatalf("再次同步应不下载、删掉混入的目录：%+v calls=%d %v", res, dl.calls, err)
	}

	v2 := bundle(t, map[string]string{"review/SKILL.md": "v2"})
	dl.bundles["/b/review/1.0.1.zip"] = v2
	if _, err = skills.Sync(context.Background(), dl, cache, dir, []api.DevenvSkill{skill("review", "1.0.1", v2)}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "review", "SKILL.md")); string(got) != "v2" {
		t.Fatalf("SKILL.md = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "review", "scripts")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("新版本里没有的文件应随整体替换消失")
	}
	if _, err := os.Stat(filepath.Join(dir, "lint")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("清单里不再有的技能（吊销 / 停用）应删除本地副本")
	}
	// 缓存按 digest：回滚到旧版本不需要再下载。
	calls := dl.calls
	if _, err = skills.Sync(context.Background(), dl, cache, dir, []api.DevenvSkill{skill("review", "1.0.0", v1)}); err != nil ||
		dl.calls != calls {
		t.Fatalf("缓存命中不应下载：calls %d→%d %v", calls, dl.calls, err)
	}
}

// TestSyncRejectsDigestMismatch 下载内容与清单 digest 不一致：报错且不写任何文件（不留目录、不进缓存）。
func TestSyncRejectsDigestMismatch(t *testing.T) {
	root := t.TempDir()
	cache, dir := filepath.Join(root, "cache"), filepath.Join(root, "skills")
	good := bundle(t, map[string]string{"review/SKILL.md": "good"})
	evil := bundle(t, map[string]string{"review/SKILL.md": "evil"})
	dl := &fakeDownloader{bundles: map[string][]byte{"/b/review/1.0.0.zip": evil}}
	_, err := skills.Sync(context.Background(), dl, cache, dir, []api.DevenvSkill{skill("review", "1.0.0", good)})
	if !errors.Is(err, skills.ErrDigestMismatch) {
		t.Fatalf("err = %v，期望 ErrDigestMismatch", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "review")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("校验失败不应写入技能目录")
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 0 {
		t.Fatal("校验失败不应写缓存")
	}
}

// TestSyncRejectsUnsafeBundles 越界条目（zip-slip）、外层目录不符、非法技能名一律拒绝。
func TestSyncRejectsUnsafeBundles(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"zip-slip":     {"review/../../escape": "x", "review/SKILL.md": "x"},
		"wrong prefix": {"other/SKILL.md": "x"},
	} {
		data := bundle(t, files)
		root := t.TempDir()
		dl := &fakeDownloader{bundles: map[string][]byte{"/b/review/1.0.0.zip": data}}
		if _, err := skills.Sync(context.Background(), dl, filepath.Join(root, "c"), filepath.Join(root, "s"),
			[]api.DevenvSkill{skill("review", "1.0.0", data)}); err == nil {
			t.Errorf("%s: 应拒绝", name)
		}
		if _, err := os.Stat(filepath.Join(root, "escape")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: 越界写出了文件", name)
		}
	}
	root := t.TempDir()
	bad := api.DevenvSkill{Name: "../x", Digest: "sha256:" + string(bytes.Repeat([]byte("0"), 64))}
	if _, err := skills.Sync(context.Background(), &fakeDownloader{}, root, filepath.Join(root, "s"),
		[]api.DevenvSkill{bad}); err == nil {
		t.Error("非法技能名应拒绝")
	}
}
