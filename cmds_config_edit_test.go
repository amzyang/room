package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amzyang/room/output"
)

// interactiveConfigApp 交互终端环境的 config 测试 app（stdin 视为 TTY）。
func interactiveConfigApp(path string) *app {
	a := newConfigTestApp(path, nil)
	a.streams.InIsTerminal = true
	return a
}

func TestConfigEditNonInteractive(t *testing.T) {
	a := newConfigTestApp(filepath.Join(t.TempDir(), "config.toml"), nil)
	_, _, err := execConfigCmd(t, a, "edit")
	if err == nil {
		t.Fatal("非交互应报错而非启动编辑器")
	}
	e := output.Classify(err)
	if e.Type != output.TypeValidation || !strings.Contains(e.Hint, "config set") {
		t.Errorf("应归 validation 且 hint 指向 config set: type=%s hint=%q", e.Type, e.Hint)
	}
}

func TestConfigEditMissingEditor(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	a := interactiveConfigApp(filepath.Join(t.TempDir(), "config.toml"))
	_, _, err := execConfigCmd(t, a, "edit")
	if err == nil {
		t.Fatal("缺 $VISUAL/$EDITOR 应报错")
	}
	e := output.Classify(err)
	if e.Type != output.TypeValidation || !strings.Contains(e.Hint, "EDITOR") {
		t.Errorf("应归 validation 且 hint 指向 EDITOR: type=%s hint=%q", e.Type, e.Hint)
	}
}

func TestConfigEditCreatesSkeletonAndRunsEditor(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vim")
	path := filepath.Join(t.TempDir(), "config.toml")
	a := interactiveConfigApp(path)
	var gotEditor, gotPath string
	a.runEditor = func(editor, p string) error {
		gotEditor, gotPath = editor, p
		return nil
	}

	out, _, err := execConfigCmd(t, a, "edit")
	if err != nil {
		t.Fatal(err)
	}
	if gotEditor != "vim" || gotPath != path {
		t.Errorf("编辑器调用 = (%q, %q), want (vim, %s)", gotEditor, gotPath, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("应先生成骨架文件: %v", err)
	}
	if !strings.Contains(string(data), "[feishu]") {
		t.Errorf("骨架应含注释结构: %s", data)
	}
	if !strings.Contains(out, "已保存") {
		t.Errorf("无警告时应确认保存: %q", out)
	}
}

func TestConfigEditVisualPrecedesEditor(t *testing.T) {
	t.Setenv("VISUAL", "nano")
	t.Setenv("EDITOR", "vim")
	a := interactiveConfigApp(filepath.Join(t.TempDir(), "config.toml"))
	var gotEditor string
	a.runEditor = func(editor, _ string) error {
		gotEditor = editor
		return nil
	}
	if _, _, err := execConfigCmd(t, a, "edit"); err != nil {
		t.Fatal(err)
	}
	if gotEditor != "nano" {
		t.Errorf("editor = %q, want nano（VISUAL 优先）", gotEditor)
	}
}

func TestConfigEditWarnsOnInvalidValueWithoutRollback(t *testing.T) {
	t.Setenv("EDITOR", "vim")
	t.Setenv("VISUAL", "")
	path := filepath.Join(t.TempDir(), "config.toml")
	a := interactiveConfigApp(path)
	bad := "[booking]\ntask_format = \"xxx,bad\"\n"
	a.runEditor = func(_, p string) error {
		return os.WriteFile(p, []byte(bad), 0o600)
	}

	_, errOut, err := execConfigCmd(t, a, "edit")
	if err != nil {
		t.Fatalf("校验问题应告警而非报错: %v", err)
	}
	if !strings.Contains(errOut, "警告") || !strings.Contains(errOut, "task_format") {
		t.Errorf("stderr 应含 task_format 警告: %q", errOut)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "xxx,bad") {
		t.Error("不应回滚用户保存的内容")
	}
}
