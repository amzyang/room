package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amzyang/room/config"
)

func newConfigTestApp(path string, entries map[string]config.Entry) *app {
	if entries == nil {
		entries = map[string]config.Entry{}
	}
	return &app{cfg: &config.Resolved{Path: path, Entries: entries}}
}

func execConfigCmd(t *testing.T, a *app, args ...string) (string, string, error) {
	t.Helper()
	cmd := newConfigCmd(a)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestConfigSetWritesAndMasksSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	a := newConfigTestApp(path, nil)

	out, _, err := execConfigCmd(t, a, "set", "feishu.app_id", "cli_test123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "feishu.app_id") || !strings.Contains(out, path) {
		t.Errorf("set 输出应含 key 与路径: %q", out)
	}

	out, _, err = execConfigCmd(t, a, "set", "FEISHU_APP_SECRET", "supersecret123")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "supersecret123") {
		t.Errorf("secret 明文不应出现在输出中: %q", out)
	}

	doc, err := config.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Values["FEISHU_APP_ID"] != "cli_test123" || doc.Values["FEISHU_APP_SECRET"] != "supersecret123" {
		t.Errorf("写入结果不符: %v", doc.Values)
	}
}

func TestConfigSetValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	a := newConfigTestApp(path, nil)

	tests := [][]string{
		{"set", "booking.room_size", "abc"},
		{"set", "feishu.auth_mode", "bogus"},
		{"set", "unknown.key", "x"},
	}
	for _, args := range tests {
		if _, _, err := execConfigCmd(t, a, args...); err == nil {
			t.Errorf("%v 应报错", args)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("校验失败时不应创建配置文件")
	}
}

func TestConfigSetWarnsOnOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	a := newConfigTestApp(path, map[string]config.Entry{
		"FEISHU_APP_ID": {Value: "shellwins", Source: config.SourceShellEnv},
	})

	_, errOut, err := execConfigCmd(t, a, "set", "feishu.app_id", "cli_new")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "shell env") || !strings.Contains(errOut, "不生效") {
		t.Errorf("应提示 shell env 覆盖: %q", errOut)
	}
}

func TestConfigGet(t *testing.T) {
	a := newConfigTestApp("unused.toml", map[string]config.Entry{
		"OPENAI_API_KEY": {Value: "sk-secret-value", Source: config.SourceTOML},
	})

	out, _, err := execConfigCmd(t, a, "get", "nlp.api_key")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "sk-secret-value" {
		t.Errorf("get 应打印生效值原文: %q", out)
	}

	out, _, err = execConfigCmd(t, a, "get", "nlp.api_key", "--source")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "config.toml") {
		t.Errorf("--source 应附加来源: %q", out)
	}
}

func TestConfigList(t *testing.T) {
	longScope := strings.Repeat("scope:x ", 20)
	a := newConfigTestApp(filepath.Join(t.TempDir(), "config.toml"), map[string]config.Entry{
		"FEISHU_APP_ID":          {Value: "cli_x", Source: config.SourceShellEnv},
		"FEISHU_APP_SECRET":      {Value: "supersecret123", Source: config.SourceTOML},
		"TASK_OWNER":             {Value: "owner", Source: config.SourceDotenv},
		"FEISHU_USER_AUTH_SCOPE": {Value: longScope, Source: config.SourceDefault},
	})

	out, _, err := execConfigCmd(t, a, "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"feishu.app_id", "FEISHU_APP_ID", "shell env", ".env", "config.toml", "不存在"} {
		if !strings.Contains(out, want) {
			t.Errorf("list 输出缺少 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "supersecret123") {
		t.Errorf("list 不应输出 secret 明文:\n%s", out)
	}
	if strings.Contains(out, longScope) {
		t.Errorf("超长值应截断显示:\n%s", out)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("截断值应带省略号:\n%s", out)
	}
}

func TestConfigUnset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	a := newConfigTestApp(path, nil)

	if _, _, err := execConfigCmd(t, a, "set", "booking.task_owner", "alice"); err != nil {
		t.Fatal(err)
	}
	out, _, err := execConfigCmd(t, a, "unset", "TASK_OWNER")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "已从") {
		t.Errorf("unset 输出不符: %q", out)
	}
	doc, err := config.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Values["TASK_OWNER"]; ok {
		t.Error("unset 后 key 仍在文件中")
	}

	out, _, err = execConfigCmd(t, a, "unset", "TASK_OWNER")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "无需删除") {
		t.Errorf("重复 unset 应提示 no-op: %q", out)
	}
}

func TestConfigPath(t *testing.T) {
	a := newConfigTestApp("/some/path/config.toml", nil)
	out, _, err := execConfigCmd(t, a, "path")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "/some/path/config.toml" {
		t.Errorf("path 输出不符: %q", out)
	}
}

// 裸 room config 打印帮助而非报错（Phase 2 起 TTY 下进 TUI）。
func TestConfigBareShowsHelp(t *testing.T) {
	out, _, err := execConfigCmd(t, newConfigTestApp("x.toml", nil))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "set") || !strings.Contains(out, "list") {
		t.Errorf("裸 config 应输出子命令帮助: %q", out)
	}
}
