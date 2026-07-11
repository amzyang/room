package feishu

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRevokeTokenSendsForm(t *testing.T) {
	var gotAuth string
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = r.ParseForm()
		gotForm = r.PostForm
		fmt.Fprint(w, `{"code":0}`)
	}))
	defer server.Close()

	client := &OAuthClient{HTTP: &http.Client{}, AppID: "cli_old", AppSecret: "old_secret", revokeURL: server.URL}
	if err := client.RevokeToken(context.Background(), "tok1", "refresh_token"); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want 空（revoke 不用 Basic auth）", gotAuth)
	}
	wantForm := map[string]string{
		"client_id":       "cli_old",
		"client_secret":   "old_secret",
		"token":           "tok1",
		"token_type_hint": "refresh_token",
	}
	for k, want := range wantForm {
		if got := gotForm.Get(k); got != want {
			t.Errorf("form[%s] = %q, want %q", k, got, want)
		}
	}
}

func TestRevokeTokenErrors(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantContains string
	}{
		{name: "HTTP 400", status: 400, body: "bad request", wantContains: "400"},
		{name: "HTTP 200 但业务 code 非 0", status: 200, body: `{"code":20063,"msg":"token not found"}`, wantContains: "20063"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			client := &OAuthClient{HTTP: &http.Client{}, AppID: "a", AppSecret: "s", revokeURL: server.URL}
			err := client.RevokeToken(context.Background(), "tok", "access_token")
			if err == nil || !strings.Contains(err.Error(), tt.wantContains) {
				t.Errorf("err = %v, want 包含 %q", err, tt.wantContains)
			}
		})
	}

	t.Run("传输错误", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		server.Close()
		client := &OAuthClient{HTTP: &http.Client{}, AppID: "a", AppSecret: "s", revokeURL: server.URL}
		if err := client.RevokeToken(context.Background(), "tok", ""); err == nil {
			t.Error("want transport error")
		}
	})
}

type fakeRevoker struct {
	calls [][2]string
	fail  map[string]error
}

func (f *fakeRevoker) RevokeToken(_ context.Context, token, hint string) error {
	f.calls = append(f.calls, [2]string{token, hint})
	return f.fail[token]
}

func TestRevokeStoredTokensBestEffort(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name      string
		token     *StoredUserToken
		fail      map[string]error
		wantCalls [][2]string
	}{
		{name: "store 空为 no-op", token: nil, wantCalls: nil},
		{
			name:      "优先撤 refresh_token，成功即止",
			token:     validToken(),
			wantCalls: [][2]string{{"valid-refresh", "refresh_token"}},
		},
		{
			name:      "refresh 失败退回 access_token",
			token:     validToken(),
			fail:      map[string]error{"valid-refresh": boom},
			wantCalls: [][2]string{{"valid-refresh", "refresh_token"}, {"valid-access", "access_token"}},
		},
		{
			name:      "两者都失败仅告警不 panic",
			token:     validToken(),
			fail:      map[string]error{"valid-refresh": boom, "valid-access": boom},
			wantCalls: [][2]string{{"valid-refresh", "refresh_token"}, {"valid-access", "access_token"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			revoker := &fakeRevoker{fail: tt.fail}
			store := &memoryStore{token: tt.token}
			RevokeStoredTokensBestEffort(context.Background(), store, revoker, slog.New(slog.DiscardHandler))
			if len(revoker.calls) != len(tt.wantCalls) {
				t.Fatalf("calls = %v, want %v", revoker.calls, tt.wantCalls)
			}
			for i, want := range tt.wantCalls {
				if revoker.calls[i] != want {
					t.Errorf("calls[%d] = %v, want %v", i, revoker.calls[i], want)
				}
			}
		})
	}
}

func TestFileUserTokenStoreDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	store := &FileUserTokenStore{Path: path}
	if err := store.Write(validToken()); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("文件仍存在: %v", err)
	}
	if err := store.Delete(); err != nil {
		t.Errorf("重复删除（文件不存在）应视为成功: %v", err)
	}
}
