package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLayering(t *testing.T) {
	shell := map[string]string{
		"FEISHU_APP_ID": "shell_id",
		"EMAIL_DOMAIN":  `"quoted.com"`, // 生效值应经 CleanEnvValue 清洗
	}
	dotenv := map[string]string{
		"FEISHU_APP_ID": "dotenv_id",
		"TASK_OWNER":    "dotenv_owner",
	}
	tomlVals := map[string]string{
		"FEISHU_APP_ID": "toml_id",
		"TASK_OWNER":    "toml_owner",
		"ROOM_LIST":     "A,B",
		"SENTRY_DSN":    "", // TOML 显式空:注入后 LookupEnv 可见,禁用编译内置
	}

	entries, inject := resolve(shell, dotenv, tomlVals)

	wantEntries := map[string]Entry{
		"FEISHU_APP_ID": {"shell_id", SourceShellEnv},   // shell 压 .env 压 TOML
		"TASK_OWNER":    {"dotenv_owner", SourceDotenv}, // .env 压 TOML
		"ROOM_LIST":     {"A,B", SourceTOML},
		"SENTRY_DSN":    {"", SourceTOML},
		"EMAIL_DOMAIN":  {"quoted.com", SourceShellEnv},
		"MODEL":         {"claude-4.6-opus", SourceDefault}, // 默认值层
		"ROOM_LEVEL_ID": {"", SourceUnset},
	}
	for k, want := range wantEntries {
		if got := entries[k]; got != want {
			t.Errorf("entries[%s] = %+v, want %+v", k, got, want)
		}
	}

	// 只注入「TOML 层生效」的 key:被 shell/.env 压住的不注入,默认值绝不注入
	wantInject := map[string]string{"ROOM_LIST": "A,B", "SENTRY_DSN": ""}
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
		"TASK_OWNER":    {"y", SourceDotenv},
		"ROOM_LIST":     {"A", SourceTOML},
		"MODEL":         {"m", SourceDefault},
	}}
	tests := []struct {
		key  string
		want Source
	}{
		{"FEISHU_APP_ID", SourceShellEnv},
		{"TASK_OWNER", SourceDotenv},
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
	envPath := filepath.Join(dir, ".env")
	tomlPath := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(envPath, []byte("TASK_OWNER=from_dotenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc := NewDocument()
	doc.Values["TASK_OWNER"] = "from_toml"
	doc.Values["ROOM_LEVEL_ID"] = "lvl_toml"
	doc.Values["SENTRY_DSN"] = ""
	if err := WriteFile(tomlPath, doc); err != nil {
		t.Fatal(err)
	}

	clearEnv(t, "TASK_OWNER", "ROOM_LEVEL_ID", "SENTRY_DSN")
	t.Setenv("FEISHU_APP_ID", "from_shell")

	r := Bootstrap(envPath, tomlPath)

	if r.Warning != "" {
		t.Fatalf("不应有告警: %s", r.Warning)
	}
	if got := os.Getenv("TASK_OWNER"); got != "from_dotenv" {
		t.Errorf("TASK_OWNER = %q, want from_dotenv(.env 压 TOML)", got)
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
	if src := r.OverrideOf("TASK_OWNER"); src != SourceDotenv {
		t.Errorf("OverrideOf(TASK_OWNER) = %v, want SourceDotenv", src)
	}
}

func TestBootstrapCorruptTOML(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(tomlPath, []byte("not toml [[["), 0o600); err != nil {
		t.Fatal(err)
	}
	clearEnv(t, "ROOM_LEVEL_ID")

	r := Bootstrap(filepath.Join(dir, ".env"), tomlPath)

	if r.Warning == "" {
		t.Error("损坏的 TOML 应产生告警而非中止")
	}
	if _, ok := os.LookupEnv("ROOM_LEVEL_ID"); ok {
		t.Error("损坏的 TOML 层不应注入任何值")
	}
}
