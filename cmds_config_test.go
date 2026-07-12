package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amzyang/room/config"
	"github.com/amzyang/room/output"
)

func newConfigTestApp(path string, entries map[string]config.Entry) *app {
	if entries == nil {
		entries = map[string]config.Entry{}
	}
	return &app{
		cfg:     &config.Resolved{Path: path, Entries: entries},
		streams: &output.Streams{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard},
	}
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
		{"set", "booking.task_format", "wedn,10:00:00-11:00:00,weekly,,拼错的星期"},
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
	longFormat := strings.Repeat("fri,11:00:00-12:00:00,weekly,alice,周会|", 5)
	a := newConfigTestApp(filepath.Join(t.TempDir(), "config.toml"), map[string]config.Entry{
		"FEISHU_APP_ID":     {Value: "cli_x", Source: config.SourceShellEnv},
		"FEISHU_APP_SECRET": {Value: "supersecret123", Source: config.SourceTOML},
		"TASK_FORMAT":       {Value: longFormat, Source: config.SourceTOML},
		"FEISHU_AUTH_MODE":  {Value: "auto", Source: config.SourceDefault},
	})

	out, _, err := execConfigCmd(t, a, "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"feishu.app_id", "FEISHU_APP_ID", "shell env", "config.toml", "默认", "不存在"} {
		if !strings.Contains(out, want) {
			t.Errorf("list 输出缺少 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "supersecret123") {
		t.Errorf("list 不应输出 secret 明文:\n%s", out)
	}
	if strings.Contains(out, longFormat) {
		t.Errorf("超长值应截断显示:\n%s", out)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("截断值应带省略号:\n%s", out)
	}
}

func TestConfigUnset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	a := newConfigTestApp(path, nil)

	if _, _, err := execConfigCmd(t, a, "set", "booking.email_domain", "corp.com"); err != nil {
		t.Fatal(err)
	}
	out, _, err := execConfigCmd(t, a, "unset", "EMAIL_DOMAIN")
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
	if _, ok := doc.Values["EMAIL_DOMAIN"]; ok {
		t.Error("unset 后 key 仍在文件中")
	}

	out, _, err = execConfigCmd(t, a, "unset", "EMAIL_DOMAIN")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "无需删除") {
		t.Errorf("重复 unset 应提示 no-op: %q", out)
	}
}

func TestConfigUnsetRemovedExtraKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := "[booking]\ntask_owner = \"alice\"\nemail_domain = \"corp.com\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newConfigTestApp(path, nil)

	out, _, err := execConfigCmd(t, a, "unset", "booking.task_owner")
	if err != nil {
		t.Fatalf("已移除键残留在文件中时 unset 应可清理: %v", err)
	}
	if !strings.Contains(out, "已从") {
		t.Errorf("unset 输出不符: %q", out)
	}
	doc, err := config.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Extra["booking"]["task_owner"]; ok {
		t.Error("unset 后 task_owner 仍残留在未识别项中")
	}
	if doc.Values["EMAIL_DOMAIN"] != "corp.com" {
		t.Errorf("其余配置不应受影响: %v", doc.Values)
	}

	// 幂等：再次 unset 提示无需删除
	out, _, err = execConfigCmd(t, a, "unset", "TASK_OWNER") // env 旧称同样可用
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

func TestConfigGetJSON(t *testing.T) {
	a := newConfigTestApp("unused.toml", map[string]config.Entry{
		"OPENAI_API_KEY": {Value: "sk-secret-value", Source: config.SourceTOML},
	})
	a.jsonOut = true

	out, _, err := execConfigCmd(t, a, "get", "nlp.api_key")
	if err != nil {
		t.Fatal(err)
	}
	got := unwrapData(t, []byte(out))
	want := map[string]any{
		"key": "nlp.api_key", "env": "OPENAI_API_KEY",
		"value": "sk-secret-value", "source": "config.toml",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("data[%s] = %v, want %v", k, got[k], v)
		}
	}
}

func TestConfigListJSON(t *testing.T) {
	a := newConfigTestApp(filepath.Join(t.TempDir(), "config.toml"), map[string]config.Entry{
		"FEISHU_APP_ID":     {Value: "cli_x", Source: config.SourceShellEnv},
		"FEISHU_APP_SECRET": {Value: "supersecret123", Source: config.SourceTOML},
	})
	a.jsonOut = true

	out, _, err := execConfigCmd(t, a, "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "supersecret123") {
		t.Errorf("list JSON 不应输出 secret 明文:\n%s", out)
	}
	var env struct {
		OK   bool       `json:"ok"`
		Data configList `json:"data"`
		Meta struct {
			Count int `json:"count"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("非法 JSON: %v: %s", err, out)
	}
	if !env.OK || env.Meta.Count != len(config.Registry) || len(env.Data.Items) != env.Meta.Count {
		t.Errorf("信封结构不符: ok=%v count=%d items=%d", env.OK, env.Meta.Count, len(env.Data.Items))
	}
	byEnv := map[string]configListItem{}
	for _, it := range env.Data.Items {
		byEnv[it.Env] = it
	}
	if it := byEnv["FEISHU_APP_ID"]; it.Source != "shell_env" || it.Value != "cli_x" {
		t.Errorf("app_id 项不符: %+v", it)
	}
	if it := byEnv["FEISHU_APP_SECRET"]; !it.Secret || it.Value == "supersecret123" {
		t.Errorf("secret 项应掩码: %+v", it)
	}
}

func TestConfigSetUnsetPathJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	a := newConfigTestApp(path, nil)
	a.jsonOut = true

	out, _, err := execConfigCmd(t, a, "set", "booking.email_domain", "corp.com")
	if err != nil {
		t.Fatal(err)
	}
	got := unwrapData(t, []byte(out))
	if got["key"] != "booking.email_domain" || got["value"] != "corp.com" || got["path"] != path {
		t.Errorf("set data 不符: %v", got)
	}

	out, _, err = execConfigCmd(t, a, "unset", "booking.email_domain")
	if err != nil {
		t.Fatal(err)
	}
	if got := unwrapData(t, []byte(out)); got["removed"] != true {
		t.Errorf("unset 已存在项 removed 应为 true: %v", got)
	}
	out, _, err = execConfigCmd(t, a, "unset", "booking.email_domain")
	if err != nil {
		t.Fatal(err)
	}
	if got := unwrapData(t, []byte(out)); got["removed"] != false {
		t.Errorf("重复 unset removed 应为 false（幂等）: %v", got)
	}

	out, _, err = execConfigCmd(t, a, "path")
	if err != nil {
		t.Fatal(err)
	}
	if got := unwrapData(t, []byte(out)); got["path"] != path {
		t.Errorf("path data 不符: %v", got)
	}
}

func TestConfigUnknownKeyIsValidation(t *testing.T) {
	a := newConfigTestApp("unused.toml", nil)
	_, _, err := execConfigCmd(t, a, "get", "unknown.key")
	if err == nil {
		t.Fatal("未知 KEY 应报错")
	}
	if got := output.ExitCode(err); got != output.ExitValidation {
		t.Errorf("未知 KEY 退出码 = %d, want %d", got, output.ExitValidation)
	}
}

// 任务编辑器的两个入口（tasks 子命令 / set 省略 VALUE）在非 TTY 下 fail-fast，
// hint 指引直接写 VALUE。
func TestConfigTasksNonInteractiveFailsFast(t *testing.T) {
	a := newConfigTestApp("unused.toml", nil)
	for _, args := range [][]string{{"tasks"}, {"set", "booking.task_format"}} {
		_, _, err := execConfigCmd(t, a, args...)
		if err == nil {
			t.Fatalf("%v 非交互应报错", args)
		}
		e := output.Classify(err)
		if e.Type != output.TypeValidation || !strings.Contains(e.Hint, "set booking.task_format") {
			t.Errorf("%v: type=%s hint=%q", args, e.Type, e.Hint)
		}
	}
}

// set 带 VALUE 仍是非交互直写路径，不受编辑器入口影响。
func TestConfigSetTaskFormatValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	a := newConfigTestApp(path, nil)
	dsl := "wed,10:00:00-11:30:00,weekly,alice:bob,周会"
	if _, _, err := execConfigCmd(t, a, "set", "booking.task_format", dsl); err != nil {
		t.Fatal(err)
	}
	doc, err := config.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Values["TASK_FORMAT"] != dsl {
		t.Errorf("写入结果不符: %q", doc.Values["TASK_FORMAT"])
	}
}

// 裸 room config --json 不能把人类帮助文本混进机读 stdout，应报 validation。
func TestConfigBareJSONIsValidation(t *testing.T) {
	a := newConfigTestApp("x.toml", nil)
	a.jsonOut = true
	_, _, err := execConfigCmd(t, a)
	if err == nil {
		t.Fatal("--json 下裸 config 应报错")
	}
	e := output.Classify(err)
	if e.Type != output.TypeValidation || !strings.Contains(e.Hint, "config list") {
		t.Errorf("应归 validation 且 hint 指引子命令: type=%s hint=%q", e.Type, e.Hint)
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
