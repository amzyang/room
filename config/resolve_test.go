package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveLayering(t *testing.T) {
	shell := map[string]string{
		"FEISHU_APP_ID": "shell_id",
		"TASK_FORMAT":   `"fri,11:00:00-12:00:00,weekly,,X"`, // 生效值应经 CleanEnvValue 清洗
	}
	tomlVals := map[string]string{
		"FEISHU_APP_ID": "toml_id",
		"EMAIL_DOMAIN":  "toml.com",
		"ROOM_LIST":     "A,B",
		"SENTRY_DSN":    "", // TOML 显式空:注入后 LookupEnv 可见,禁用编译内置
	}

	entries, inject := resolve(shell, tomlVals)

	wantEntries := map[string]Entry{
		"FEISHU_APP_ID": {"shell_id", SourceShellEnv}, // shell 压 TOML
		"EMAIL_DOMAIN":  {"toml.com", SourceTOML},
		"ROOM_LIST":     {"A,B", SourceTOML},
		"SENTRY_DSN":    {"", SourceTOML},
		"TASK_FORMAT":   {"fri,11:00:00-12:00:00,weekly,,X", SourceShellEnv},
		"MODEL":         {"claude-4.6-opus", SourceDefault}, // 默认值层
		"ROOM_LEVEL_ID": {"", SourceUnset},
	}
	for k, want := range wantEntries {
		if got := entries[k]; got != want {
			t.Errorf("entries[%s] = %+v, want %+v", k, got, want)
		}
	}

	// 只注入「TOML 层生效」的 key:被 shell 压住的不注入,默认值绝不注入
	wantInject := map[string]string{"EMAIL_DOMAIN": "toml.com", "ROOM_LIST": "A,B", "SENTRY_DSN": ""}
	if len(inject) != len(wantInject) {
		t.Errorf("inject = %v, want %v", inject, wantInject)
	}
	for k, v := range wantInject {
		if got, ok := inject[k]; !ok || got != v {
			t.Errorf("inject[%s] = %q(存在=%v), want %q", k, got, ok, v)
		}
	}
}

func TestOverrideOf(t *testing.T) {
	r := &Resolved{Entries: map[string]Entry{
		"FEISHU_APP_ID": {"x", SourceShellEnv},
		"ROOM_LIST":     {"A", SourceTOML},
		"MODEL":         {"m", SourceDefault},
	}}
	tests := []struct {
		key  string
		want Source
	}{
		{"FEISHU_APP_ID", SourceShellEnv},
		{"ROOM_LIST", SourceUnset},
		{"MODEL", SourceUnset},
		{"NOT_A_KEY", SourceUnset},
	}
	for _, tt := range tests {
		if got := r.OverrideOf(tt.key); got != tt.want {
			t.Errorf("OverrideOf(%s) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

// clearEnv 注销 key 并注册测试结束后的恢复(t.Setenv 先记录原值)。
func clearEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func TestBootstrap(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "config.toml")

	doc := NewDocument()
	doc.Values["EMAIL_DOMAIN"] = "from_toml"
	doc.Values["ROOM_LEVEL_ID"] = "lvl_toml"
	doc.Values["SENTRY_DSN"] = ""
	if err := WriteFile(tomlPath, doc); err != nil {
		t.Fatal(err)
	}

	clearEnv(t, "EMAIL_DOMAIN", "ROOM_LEVEL_ID", "SENTRY_DSN")
	t.Setenv("FEISHU_APP_ID", "from_shell")

	r := Bootstrap(tomlPath)

	if r.Warning != "" {
		t.Fatalf("不应有告警: %s", r.Warning)
	}
	if got := os.Getenv("EMAIL_DOMAIN"); got != "from_toml" {
		t.Errorf("EMAIL_DOMAIN = %q, want from_toml(TOML 注入)", got)
	}
	if got := os.Getenv("ROOM_LEVEL_ID"); got != "lvl_toml" {
		t.Errorf("ROOM_LEVEL_ID = %q, want lvl_toml(TOML 注入)", got)
	}
	if v, ok := os.LookupEnv("SENTRY_DSN"); !ok || v != "" {
		t.Errorf("SENTRY_DSN 应被注入为显式空(禁用编译内置), got %q(存在=%v)", v, ok)
	}
	if e := r.Entries["FEISHU_APP_ID"]; e != (Entry{"from_shell", SourceShellEnv}) {
		t.Errorf("FEISHU_APP_ID entry = %+v", e)
	}
	if src := r.OverrideOf("FEISHU_APP_ID"); src != SourceShellEnv {
		t.Errorf("OverrideOf(FEISHU_APP_ID) = %v, want SourceShellEnv", src)
	}
}

func TestBootstrapWarnsRemovedTaskOwner(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "config.toml")
	content := "[booking]\ntask_owner = \"alice\"\n"
	if err := os.WriteFile(tomlPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	clearEnv(t, "TASK_OWNER")

	r := Bootstrap(tomlPath)

	if !strings.Contains(r.Warning, "task_owner") || !strings.Contains(r.Warning, "room config unset") {
		t.Errorf("残留已移除键应产生带清理指引的告警: %q", r.Warning)
	}
	if _, ok := os.LookupEnv("TASK_OWNER"); ok {
		t.Error("已移除键不应注入进程 env")
	}
}

func TestBootstrapCorruptTOML(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(tomlPath, []byte("not toml [[["), 0o600); err != nil {
		t.Fatal(err)
	}
	clearEnv(t, "ROOM_LEVEL_ID")

	r := Bootstrap(tomlPath)

	if r.Warning == "" {
		t.Error("损坏的 TOML 应产生告警而非中止")
	}
	if _, ok := os.LookupEnv("ROOM_LEVEL_ID"); ok {
		t.Error("损坏的 TOML 层不应注入任何值")
	}
}
