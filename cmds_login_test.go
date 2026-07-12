package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/amzyang/room/feishu"
	"github.com/amzyang/room/output"
)

func testDeviceAuthorization() *feishu.DeviceAuthorization {
	return &feishu.DeviceAuthorization{
		DeviceCode:              "dc1",
		UserCode:                "WDJB-MJHT",
		VerificationURI:         "https://v.example/login",
		VerificationURIComplete: "https://v.example/login?user_code=WDJB-MJHT",
		ExpiresInSec:            300,
		IntervalSec:             5,
	}
}

func TestEmitDeviceAuthorizationJSON(t *testing.T) {
	var buf bytes.Buffer
	emitDeviceAuthorization(&buf, testDeviceAuthorization(), true, true)

	got := unwrapData(t, buf.Bytes())
	want := map[string]any{
		"event":          "device_authorization",
		"device_code":    "dc1",
		"expires_in":     float64(300),
		"interval":       float64(5),
		"resume_command": "room login --device-code dc1",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("data[%s] = %v, want %v", k, got[k], v)
		}
	}
}

func TestEmitDeviceAuthorizationJSONNoResume(t *testing.T) {
	var buf bytes.Buffer
	emitDeviceAuthorization(&buf, testDeviceAuthorization(), true, false)
	if got := unwrapData(t, buf.Bytes()); got["resume_command"] != nil {
		t.Errorf("非 --no-wait 不应含 resume_command: %v", got)
	}
}

func TestEmitDeviceAuthorizationText(t *testing.T) {
	var buf bytes.Buffer
	emitDeviceAuthorization(&buf, testDeviceAuthorization(), false, true)
	out := buf.String()
	for _, want := range []string{
		"https://v.example/login?user_code=WDJB-MJHT",
		"room login --device-code dc1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("文本输出缺少 %q:\n%s", want, out)
		}
	}
}

func TestEmitLoginOKJSONNoTokenLeak(t *testing.T) {
	var buf bytes.Buffer
	token := &feishu.StoredUserToken{
		AccessToken: "u-secret-access", RefreshToken: "r-secret-refresh",
		OpenID: "ou_1", Scope: "calendar:calendar", AuthExpireAt: 1791800000000,
	}
	if err := emitLoginOK(&buf, token, true); err != nil {
		t.Fatal(err)
	}
	got := unwrapData(t, buf.Bytes())
	if got["event"] != "login_ok" || got["open_id"] != "ou_1" || got["auth_expire_at_ms"] != float64(1791800000000) {
		t.Errorf("data 不符: %v", got)
	}
	if strings.Contains(buf.String(), "secret") {
		t.Errorf("login_ok 事件泄漏 token:\n%s", buf.String())
	}
}

func TestClassifyLoginErr(t *testing.T) {
	e := output.Classify(classifyLoginErr(feishu.ErrLoginExpired))
	if e.Type != output.TypeAuth || !e.Retryable {
		t.Errorf("过期应归 auth 且可重试: %+v", e)
	}
	if output.ExitCode(e) != output.ExitAuth {
		t.Errorf("退出码 = %d, want 3", output.ExitCode(e))
	}
}

func TestLoginResumeFlagsMutuallyExclusive(t *testing.T) {
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	_, err := execAppCmd(t, a, newLoginCmd, "--no-wait", "--device-code", "dc")
	if err == nil || output.ExitCode(err) != output.ExitValidation {
		t.Fatalf("--no-wait 与 --device-code 互斥应归 validation: %v", err)
	}
}

func TestLoginMissingCredentialsIsConfigError(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "")
	t.Setenv("FEISHU_APP_SECRET", "")
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	_, err := execAppCmd(t, a, newLoginCmd, "--no-wait")
	if err == nil || output.ExitCode(err) != output.ExitAuth {
		t.Fatalf("缺凭证应归 config（exit 3）: %v", err)
	}
}
