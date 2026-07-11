package feishu

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newTestRegistrar(serverURL string, waits *[]time.Duration) *AppRegistrar {
	return &AppRegistrar{
		HTTP:            &http.Client{},
		Log:             slog.New(slog.DiscardHandler),
		Clock:           func() time.Time { return time.UnixMilli(nowMs) },
		registrationURL: serverURL,
		sleep: func(_ context.Context, d time.Duration) error {
			*waits = append(*waits, d)
			return nil
		},
	}
}

func TestBeginSendsAnonymousForm(t *testing.T) {
	var gotAuth, gotContentType string
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		gotForm = r.PostForm
		fmt.Fprint(w, `{"device_code":"dc1","user_code":"WDJB-MJHT","verification_uri":"https://v.example/cli","verification_uri_complete":"https://v.example/cli?user_code=WDJB-MJHT","expires_in":600,"interval":7}`)
	}))
	defer server.Close()

	var waits []time.Duration
	code, err := newTestRegistrar(server.URL, &waits).Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want 匿名（空）", gotAuth)
	}
	if !strings.HasPrefix(gotContentType, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	wantForm := map[string]string{
		"action":            "begin",
		"archetype":         "PersonalAgent",
		"auth_method":       "client_secret",
		"request_user_info": "open_id tenant_brand",
	}
	for k, want := range wantForm {
		if got := gotForm.Get(k); got != want {
			t.Errorf("form[%s] = %q, want %q", k, got, want)
		}
	}
	if code.DeviceCode != "dc1" || code.UserCode != "WDJB-MJHT" ||
		code.VerificationURI != "https://v.example/cli" ||
		code.VerificationURIComplete != "https://v.example/cli?user_code=WDJB-MJHT" ||
		code.ExpiresInSec != 600 || code.IntervalSec != 7 {
		t.Errorf("code = %+v", code)
	}
}

func TestBeginFillsDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"device_code":"dc","user_code":"AB CD+"}`)
	}))
	defer server.Close()

	var waits []time.Duration
	reg := newTestRegistrar(server.URL, &waits)
	reg.openBaseURL = "https://example.com"
	code, err := reg.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if code.ExpiresInSec != 300 || code.IntervalSec != 5 {
		t.Errorf("defaults = %d/%d, want 300/5", code.ExpiresInSec, code.IntervalSec)
	}
	want := "https://example.com/page/cli?user_code=AB+CD%2B"
	if code.VerificationURIComplete != want {
		t.Errorf("VerificationURIComplete = %q, want %q", code.VerificationURIComplete, want)
	}
}

func TestBeginErrors(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantContains string
	}{
		{name: "HTTP 400 带 error JSON", status: 400, body: `{"error":"invalid_request","error_description":"bad archetype"}`, wantContains: "bad archetype"},
		{name: "HTTP 200 但 body 带 error", status: 200, body: `{"error":"rate_limited"}`, wantContains: "rate_limited"},
		{name: "非 JSON 响应", status: 500, body: "oops", wantContains: "500"},
		{name: "HTTP 200 但缺 device_code", status: 200, body: `{}`, wantContains: "device_code"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			var waits []time.Duration
			_, err := newTestRegistrar(server.URL, &waits).Begin(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.wantContains) {
				t.Errorf("err = %v, want 包含 %q", err, tt.wantContains)
			}
		})
	}
}

const pollSuccessBody = `{"client_id":"cli_new","client_secret":"sec_new","user_info":{"open_id":"ou_1","tenant_brand":"feishu"}}`

func TestPollPendingThenSuccess(t *testing.T) {
	calls := 0
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = r.ParseForm()
		gotForm = r.PostForm
		if calls == 1 {
			fmt.Fprint(w, `{"error":"authorization_pending"}`)
			return
		}
		fmt.Fprint(w, pollSuccessBody)
	}))
	defer server.Close()

	var waits []time.Duration
	creds, err := newTestRegistrar(server.URL, &waits).Poll(context.Background(), "dc1", 5, 300)
	if err != nil {
		t.Fatal(err)
	}
	if gotForm.Get("action") != "poll" || gotForm.Get("device_code") != "dc1" {
		t.Errorf("form = %v", gotForm)
	}
	if creds.AppID != "cli_new" || creds.AppSecret != "sec_new" || creds.OpenID != "ou_1" || creds.TenantBrand != "feishu" {
		t.Errorf("creds = %+v", creds)
	}
	wantWaits := []time.Duration{5 * time.Second, 5 * time.Second}
	if len(waits) != 2 || waits[0] != wantWaits[0] || waits[1] != wantWaits[1] {
		t.Errorf("waits = %v, want %v", waits, wantWaits)
	}
}

func TestPollBackoff(t *testing.T) {
	tests := []struct {
		name      string
		firstBody string
		wantWaits []time.Duration
	}{
		{name: "slow_down 间隔加 5 秒", firstBody: `{"error":"slow_down"}`, wantWaits: []time.Duration{5 * time.Second, 10 * time.Second}},
		{name: "瞬时非 JSON 错误间隔加 1 秒", firstBody: "not json", wantWaits: []time.Duration{5 * time.Second, 6 * time.Second}},
		{name: "无 error 无 client_id 的空 JSON 按瞬时重试", firstBody: `{}`, wantWaits: []time.Duration{5 * time.Second, 6 * time.Second}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if calls == 1 {
					fmt.Fprint(w, tt.firstBody)
					return
				}
				fmt.Fprint(w, pollSuccessBody)
			}))
			defer server.Close()

			var waits []time.Duration
			if _, err := newTestRegistrar(server.URL, &waits).Poll(context.Background(), "dc", 5, 300); err != nil {
				t.Fatal(err)
			}
			if len(waits) != len(tt.wantWaits) || waits[0] != tt.wantWaits[0] || waits[1] != tt.wantWaits[1] {
				t.Errorf("waits = %v, want %v", waits, tt.wantWaits)
			}
		})
	}
}

func TestPollTerminalErrors(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantSentinel error
		wantContains string
	}{
		{name: "access_denied", body: `{"error":"access_denied"}`, wantSentinel: ErrRegistrationDenied},
		{name: "expired_token", body: `{"error":"expired_token"}`, wantSentinel: ErrRegistrationExpired},
		{name: "invalid_grant", body: `{"error":"invalid_grant","error_description":"grant gone"}`, wantSentinel: ErrRegistrationExpired, wantContains: "grant gone"},
		{name: "未知错误码直接失败", body: `{"error":"server_error","error_description":"boom"}`, wantContains: "boom"},
		{name: "lark 租户", body: `{"client_id":"cli_x","user_info":{"tenant_brand":"lark"}}`, wantSentinel: ErrLarkTenantNotSupported},
		{name: "feishu 缺 client_secret", body: `{"client_id":"cli_x","user_info":{"tenant_brand":"feishu"}}`, wantContains: "client_secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			var waits []time.Duration
			_, err := newTestRegistrar(server.URL, &waits).Poll(context.Background(), "dc", 5, 300)
			if err == nil {
				t.Fatal("want error")
			}
			if tt.wantSentinel != nil && !errors.Is(err, tt.wantSentinel) {
				t.Errorf("err = %v, want errors.Is(%v)", err, tt.wantSentinel)
			}
			if tt.wantContains != "" && !strings.Contains(err.Error(), tt.wantContains) {
				t.Errorf("err = %v, want 包含 %q", err, tt.wantContains)
			}
			if tt.name == "feishu 缺 client_secret" && errors.Is(err, ErrLarkTenantNotSupported) {
				t.Errorf("feishu 租户不应命中 lark 哨兵: %v", err)
			}
		})
	}
}

func TestPollTimeoutByAttempts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"error":"authorization_pending"}`)
	}))
	defer server.Close()

	var waits []time.Duration
	reg := newTestRegistrar(server.URL, &waits)
	reg.maxPollAttempts = 2
	_, err := reg.Poll(context.Background(), "dc", 5, 300)
	if !errors.Is(err, ErrRegistrationExpired) {
		t.Errorf("err = %v, want ErrRegistrationExpired", err)
	}
	if len(waits) != 2 {
		t.Errorf("attempts = %d, want 2", len(waits))
	}
}

func TestPollCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"error":"authorization_pending"}`)
	}))
	defer server.Close()

	var waits []time.Duration
	reg := newTestRegistrar(server.URL, &waits)
	reg.sleep = func(context.Context, time.Duration) error { return context.Canceled }
	_, err := reg.Poll(context.Background(), "dc", 5, 300)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want 包裹 context.Canceled", err)
	}
}
