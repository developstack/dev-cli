package config_test

import (
	"path/filepath"
	"testing"

	"github.com/developstack/aidevstack/cli/internal/config"
)

// TestNormalizePlatform 只保留 scheme://host[:port]；默认 https；明文 http 只允许回环或显式放行。
func TestNormalizePlatform(t *testing.T) {
	ok := map[string]string{
		"ai.example.com":                  "https://ai.example.com",
		"https://AI.example.com/app/x?y":  "https://ai.example.com",
		"https://ai.example.com:8443/":    "https://ai.example.com:8443",
		"http://localhost:8080":           "http://localhost:8080",
		"http://127.0.0.1:18080/whatever": "http://127.0.0.1:18080",
	}
	for in, want := range ok {
		got, err := config.NormalizePlatform(in)
		if err != nil || got != want {
			t.Errorf("NormalizePlatform(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"http://10.0.0.5:8080", "ftp://x", "https://"} {
		if _, err := config.NormalizePlatform(in); err == nil {
			t.Errorf("NormalizePlatform(%q) 应拒绝", in)
		}
	}
	t.Setenv("AIDEVSTACK_ALLOW_INSECURE_HTTP", "1")
	if got, err := config.NormalizePlatform("http://10.0.0.5:8080"); err != nil || got != "http://10.0.0.5:8080" {
		t.Fatalf("显式放行后应接受内网 http：%q %v", got, err)
	}
}

// TestResolvePlatformPrecedence 参数 > 环境变量 > 配置文件；都没有时提示 login --platform。
func TestResolvePlatformPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, config.Config{Platform: "https://saved.example.com"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.Platform != "https://saved.example.com" {
		t.Fatalf("Load = %+v %v", cfg, err)
	}
	t.Setenv("AIDEVSTACK_PLATFORM", "")
	if got, _ := config.ResolvePlatform("", cfg); got != "https://saved.example.com" {
		t.Fatalf("配置文件 = %q", got)
	}
	t.Setenv("AIDEVSTACK_PLATFORM", "env.example.com")
	if got, _ := config.ResolvePlatform("", cfg); got != "https://env.example.com" {
		t.Fatalf("环境变量 = %q", got)
	}
	if got, _ := config.ResolvePlatform("flag.example.com", cfg); got != "https://flag.example.com" {
		t.Fatalf("参数 = %q", got)
	}
	t.Setenv("AIDEVSTACK_PLATFORM", "")
	if _, err := config.ResolvePlatform("", config.Config{}); err == nil {
		t.Fatal("没有平台地址应报错")
	}
}
