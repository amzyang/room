package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/amzyang/room/config"
	"github.com/amzyang/room/output"
)

// execRootCmd 从 newRootCmd 组装完整命令树执行（--sentry-dsn= 禁用上报）。
func execRootCmd(t *testing.T, args ...string) (string, *app, error) {
	t.Helper()
	root, a := newRootCmd(&config.Resolved{Entries: map[string]config.Entry{}})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append([]string{"--sentry-dsn="}, args...))
	err := root.Execute()
	return out.String(), a, err
}

func TestUnknownSubcommandIsValidation(t *testing.T) {
	_, _, err := execRootCmd(t, "bogus")
	if err == nil {
		t.Fatal("未知子命令应报错")
	}
	if e := output.Classify(err); e.Type != output.TypeValidation {
		t.Errorf("error.type = %s, want validation", e.Type)
	}
	if got := output.ExitCode(err); got != output.ExitValidation {
		t.Errorf("退出码 = %d, want %d", got, output.ExitValidation)
	}
}

func TestUnknownSubcommandParsesJSONFlag(t *testing.T) {
	// --json 必须在未知子命令路径上生效，main 才能输出错误信封而非人类文本
	_, a, err := execRootCmd(t, "bogus", "--json")
	if err == nil || !a.jsonOut {
		t.Fatalf("未知子命令应报错且 --json 应被解析: err=%v jsonOut=%v", err, a.jsonOut)
	}
}

func TestUnknownSubcommandSuggests(t *testing.T) {
	_, _, err := execRootCmd(t, "bok") // 接近 book
	if err == nil {
		t.Fatal("未知子命令应报错")
	}
	if hint := output.Classify(err).Hint; !strings.Contains(hint, "book") {
		t.Errorf("hint 应给出相近命令建议: %q", hint)
	}
}

func TestUnknownFlagIsValidationWithHelpHint(t *testing.T) {
	// agent 误用 flag 时纠正信号只有 stderr 一个通道：解析错误须归 validation
	// （exit 2）且 hint 指向 <cmd> --help（对齐 kubectl 惯例）
	_, _, err := execRootCmd(t, "book", "--app", "x")
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --app") {
		t.Fatalf("应返回 unknown flag 错误: %v", err)
	}
	e := output.Classify(err)
	if e.Type != output.TypeValidation {
		t.Errorf("error.type = %s, want validation", e.Type)
	}
	if !strings.Contains(e.Hint, "room book --help") {
		t.Errorf("hint 应指向 room book --help: %q", e.Hint)
	}
	if got := output.ExitCode(err); got != output.ExitValidation {
		t.Errorf("退出码 = %d, want %d", got, output.ExitValidation)
	}
}

func TestJSONRequestedFallback(t *testing.T) {
	// pflag 在首个坏 flag 处停止解析：--json 放末尾（agent 习惯）时 a.jsonOut
	// 不生效，main 靠 jsonRequested 兜底保证错误信封仍是 JSON
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"book", "--badflag", "--json"}, true},
		{[]string{"book", "-d", "07-15", "--badflag", "--json"}, true},
		{[]string{"book", "--json=false"}, false},
		{[]string{"book", "--", "--json"}, false}, // -- 之后是位置参数
		{[]string{"list"}, false},
	}
	for _, c := range cases {
		if got := jsonRequested(c.args); got != c.want {
			t.Errorf("jsonRequested(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}

func TestBareRootShowsHelp(t *testing.T) {
	out, _, err := execRootCmd(t)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Available Commands") {
		t.Errorf("裸 room 应输出帮助: %q", out)
	}
}

func TestDryRunRejectedOutsideAuto(t *testing.T) {
	for _, args := range [][]string{
		{"book", "--dryrun", "-d", "07-15", "-t", "14:00-15:00"},
		{"cancel", "--dryrun", "--event-id", "x", "--yes"},
	} {
		_, _, err := execRootCmd(t, args...)
		if err == nil {
			t.Fatalf("%v 应拒绝 --dryrun", args)
		}
		e := output.Classify(err)
		if e.Type != output.TypeValidation || !strings.Contains(e.Hint, "auto") {
			t.Errorf("%v: 应归 validation 且 hint 指向 auto，实际 type=%s hint=%q", args, e.Type, e.Hint)
		}
	}
}

func TestDryRunAllowedOnAuto(t *testing.T) {
	// 守卫放行 auto；后续因缺凭证归 config（exit 3），证明没被 --dryrun 校验拦下
	t.Setenv("FEISHU_APP_ID", "")
	t.Setenv("FEISHU_APP_SECRET", "")
	_, _, err := execRootCmd(t, "auto", "--dryrun")
	if err == nil {
		t.Fatal("缺凭证应报错")
	}
	if got := output.ExitCode(err); got != output.ExitAuth {
		t.Errorf("auto --dryrun 应过守卫后因缺凭证 exit 3，实际 %d: %v", got, err)
	}
}
