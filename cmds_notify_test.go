package main

import (
	"strings"
	"testing"

	"github.com/amzyang/room/output"
)

func TestNotifyMissingMessageFailsFast(t *testing.T) {
	t.Setenv("FEISHU_BOT_WEBHOOK", "https://example.com/hook")
	a := newAgentTestApp(t, nil, "")
	_, err := execAppCmd(t, a, newNotifyCmd)
	if err == nil {
		t.Fatal("缺消息文本应 exit 2，而非静默发送测试消息")
	}
	e := output.Classify(err)
	if e.Type != output.TypeValidation {
		t.Errorf("error.type = %s, want validation", e.Type)
	}
	if !strings.Contains(e.Hint, "--test") {
		t.Errorf("hint 应指引 --test: %q", e.Hint)
	}
}

func TestNotifyBlankMessageFailsFast(t *testing.T) {
	t.Setenv("FEISHU_BOT_WEBHOOK", "https://example.com/hook")
	a := newAgentTestApp(t, nil, "")
	_, err := execAppCmd(t, a, newNotifyCmd, "   ")
	if err == nil {
		t.Fatal("空白消息应 exit 2（脚本变量意外为空的陷阱）")
	}
	if got := output.ExitCode(err); got != output.ExitValidation {
		t.Errorf("退出码 = %d, want %d", got, output.ExitValidation)
	}
}

func TestNotifyTestConflictsWithMessage(t *testing.T) {
	t.Setenv("FEISHU_BOT_WEBHOOK", "https://example.com/hook")
	a := newAgentTestApp(t, nil, "")
	_, err := execAppCmd(t, a, newNotifyCmd, "--test", "hi")
	if err == nil {
		t.Fatal("--test 与消息文本同给应 exit 2")
	}
	if e := output.Classify(err); e.Type != output.TypeValidation {
		t.Errorf("error.type = %s, want validation", e.Type)
	}
}

func TestNotifyTestMissingWebhookIsConfigError(t *testing.T) {
	t.Setenv("FEISHU_BOT_WEBHOOK", "")
	a := newAgentTestApp(t, nil, "")
	_, err := execAppCmd(t, a, newNotifyCmd, "--test")
	if err == nil {
		t.Fatal("缺 webhook 配置应报错")
	}
	if e := output.Classify(err); e.Type != output.TypeConfig {
		t.Errorf("error.type = %s, want config", e.Type)
	}
}
