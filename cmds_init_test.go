package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amzyang/room/config"
	"github.com/amzyang/room/feishu"
)

func TestValidateResumeFlags(t *testing.T) {
	if err := validateResumeFlags(true, "dc"); err == nil {
		t.Error("--no-wait 与 --device-code 互斥应报错")
	}
	if err := validateResumeFlags(true, ""); err != nil {
		t.Errorf("仅 --no-wait 应放行: %v", err)
	}
	if err := validateResumeFlags(false, "dc"); err != nil {
		t.Errorf("仅 --device-code 应放行: %v", err)
	}
}

func TestCheckExistingAppCredentials(t *testing.T) {
	tests := []struct {
		name    string
		appID   string
		secret  string
		force   bool
		wantErr bool
	}{
		{name: "全新配置放行"},
		{name: "已有 app_id 拒绝", appID: "cli_a1b2c3d4e5", wantErr: true},
		{name: "仅有 secret 也拒绝", secret: "s3cr3tvalue", wantErr: true},
		{name: "force 放行", appID: "cli_a1b2c3d4e5", force: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkExistingAppCredentials(tt.appID, tt.secret, tt.force)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if err == nil {
				return
			}
			if tt.appID != "" {
				if strings.Contains(err.Error(), tt.appID) {
					t.Errorf("错误消息泄漏完整 app_id: %v", err)
				}
				if !strings.Contains(err.Error(), maskSecret(tt.appID)) {
					t.Errorf("错误消息应含脱敏 app_id %q: %v", maskSecret(tt.appID), err)
				}
			}
			if tt.secret != "" && strings.Contains(err.Error(), tt.secret) {
				t.Errorf("错误消息泄漏 secret: %v", err)
			}
		})
	}
}

func TestMaskSecret(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"short", "****"},
		{"cli_a1b2c3d4e5", "****d4e5"},
	}
	for _, tt := range tests {
		if got := maskSecret(tt.in); got != tt.want {
			t.Errorf("maskSecret(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// unwrapData 解析成功信封并断言 ok:true，返回 data 对象。
func unwrapData(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var env struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("非法 JSON: %v: %s", err, raw)
	}
	if !env.OK {
		t.Fatalf("信封 ok 应为 true: %s", raw)
	}
	return env.Data
}

func testRegistrationCode() *feishu.AppRegistrationCode {
	return &feishu.AppRegistrationCode{
		DeviceCode:              "dc1",
		UserCode:                "WDJB-MJHT",
		VerificationURI:         "https://v.example/cli",
		VerificationURIComplete: "https://v.example/cli?user_code=WDJB-MJHT",
		ExpiresInSec:            300,
		IntervalSec:             5,
	}
}

func TestEmitAppRegistrationJSON(t *testing.T) {
	var buf bytes.Buffer
	emitAppRegistration(&buf, testRegistrationCode(), true, false, false)

	got := unwrapData(t, buf.Bytes())
	want := map[string]any{
		"event":                     "app_registration",
		"device_code":               "dc1",
		"user_code":                 "WDJB-MJHT",
		"verification_uri":          "https://v.example/cli",
		"verification_uri_complete": "https://v.example/cli?user_code=WDJB-MJHT",
		"expires_in":                float64(300),
		"interval":                  float64(5),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("json[%s] = %v, want %v", k, got[k], v)
		}
	}
	if _, ok := got["resume_command"]; ok {
		t.Errorf("非 --no-wait 模式 JSON 不应含 resume_command: %v", got)
	}
}

func TestEmitAppRegistrationJSONResumeCommand(t *testing.T) {
	var buf bytes.Buffer
	emitAppRegistration(&buf, testRegistrationCode(), true, true, true)

	got := unwrapData(t, buf.Bytes())
	// --no-wait --force 的第二段是独立进程，JSON 消费者必须能从事件里拿到完整恢复命令（含 --force）
	if want := "room init --device-code dc1 --force"; got["resume_command"] != want {
		t.Errorf("resume_command = %v, want %q", got["resume_command"], want)
	}
}

func TestEmitAppRegistrationText(t *testing.T) {
	var buf bytes.Buffer
	emitAppRegistration(&buf, testRegistrationCode(), false, true, true)
	out := buf.String()

	for _, want := range []string{
		"https://v.example/cli?user_code=WDJB-MJHT",
		"WDJB-MJHT",
		"https://v.example/cli",
		"room init --device-code dc1 --force",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("文本输出缺少 %q:\n%s", want, out)
		}
	}

	buf.Reset()
	emitAppRegistration(&buf, testRegistrationCode(), false, false, false)
	if strings.Contains(buf.String(), "--device-code") {
		t.Errorf("非 --no-wait 模式不应打印恢复命令:\n%s", buf.String())
	}
}

func TestSaveAppCredentials(t *testing.T) {
	creds := &feishu.AppCredentials{AppID: "cli_new", AppSecret: "sec_new", OpenID: "ou_1", TenantBrand: "feishu"}

	t.Run("文本模式保留已有配置", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		doc := config.NewDocument()
		doc.Values["EMAIL_DOMAIN"] = "keepme"
		if err := config.WriteFile(path, doc); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := saveAppCredentials(&buf, path, creds, false); err != nil {
			t.Fatal(err)
		}
		got, err := config.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got.Values["FEISHU_APP_ID"] != "cli_new" || got.Values["FEISHU_APP_SECRET"] != "sec_new" {
			t.Errorf("凭证未写入 TOML: %v", got.Values)
		}
		if got.Values["EMAIL_DOMAIN"] != "keepme" {
			t.Errorf("已有配置项被覆盖丢失: %v", got.Values)
		}
		out := buf.String()
		if !strings.Contains(out, "cli_new") || !strings.Contains(out, "room login") {
			t.Errorf("文本输出缺少 app_id 或 room login 提示:\n%s", out)
		}
		if strings.Contains(out, "sec_new") {
			t.Errorf("文本输出泄漏 secret:\n%s", out)
		}
	})

	t.Run("写入失败时错误含 app_id 找回指引", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte("not toml [[["), 0o600); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		err := saveAppCredentials(&buf, path, creds, false)
		if err == nil {
			t.Fatal("want error")
		}
		if !strings.Contains(err.Error(), "cli_new") {
			t.Errorf("写失败错误应含 app_id 供找回: %v", err)
		}
		if strings.Contains(err.Error(), "sec_new") {
			t.Errorf("错误消息泄漏 secret: %v", err)
		}
	})

	t.Run("JSON 模式", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		var buf bytes.Buffer
		if err := saveAppCredentials(&buf, path, creds, true); err != nil {
			t.Fatal(err)
		}
		got := unwrapData(t, buf.Bytes())
		want := map[string]any{
			"event":        "app_registered",
			"app_id":       "cli_new",
			"open_id":      "ou_1",
			"tenant_brand": "feishu",
			"env_path":     path,
			"dest":         "toml",
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("json[%s] = %v, want %v", k, got[k], v)
			}
		}
		if strings.Contains(buf.String(), "sec_new") {
			t.Errorf("JSON 事件泄漏 secret:\n%s", buf.String())
		}
	})
}
