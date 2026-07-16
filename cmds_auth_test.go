package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amzyang/room/feishu"
	"github.com/amzyang/room/output"
)

// fakeTokenStore 内存 userTokenStore。
type fakeTokenStore struct {
	token  *feishu.StoredUserToken
	delErr error
}

func (f *fakeTokenStore) Read() *feishu.StoredUserToken         { return f.token }
func (f *fakeTokenStore) Write(t *feishu.StoredUserToken) error { f.token = t; return nil }
func (f *fakeTokenStore) Delete() error {
	if f.delErr != nil {
		return f.delErr
	}
	f.token = nil
	return nil
}

// fakeTokenRevoker 记录撤销调用。
type fakeTokenRevoker struct {
	calls int
	err   error
}

func (f *fakeTokenRevoker) RevokeToken(context.Context, string, string) error {
	f.calls++
	return f.err
}

// authTestToken 相对测试 app 的 now（2026-07-10 09:00）构造有效凭证。
func authTestToken(a *app) *feishu.StoredUserToken {
	now := a.now()
	return &feishu.StoredUserToken{
		AccessToken: "at", RefreshToken: "rt",
		AccessExpireAt:  now.Add(2 * time.Hour).UnixMilli(),
		RefreshExpireAt: now.Add(30 * 24 * time.Hour).UnixMilli(),
		AuthExpireAt:    now.Add(200 * 24 * time.Hour).UnixMilli(),
		OpenID:          "ou_1", UserID: "u_1", Name: "张三",
		Scope: "calendar:calendar",
	}
}

func TestWhoamiNotLoggedIn(t *testing.T) {
	a := newAgentTestApp(t, nil, "")
	a.tokenStore = &fakeTokenStore{}
	_, err := execAppCmd(t, a, newWhoamiCmd)
	if err == nil {
		t.Fatal("未登录应报错")
	}
	e := output.Classify(err)
	if e.Type != output.TypeAuth {
		t.Errorf("error.type = %s, want auth", e.Type)
	}
	if got := output.ExitCode(err); got != output.ExitAuth {
		t.Errorf("退出码 = %d, want %d", got, output.ExitAuth)
	}
	if !strings.Contains(e.Hint, "room login") {
		t.Errorf("hint 应指引 room login: %q", e.Hint)
	}
}

func TestWhoamiJSON(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "cli_test")
	a := newAgentTestApp(t, nil, "")
	a.tokenStore = &fakeTokenStore{token: authTestToken(a)}
	a.jsonOut = true

	out, err := execAppCmd(t, a, newWhoamiCmd)
	if err != nil {
		t.Fatal(err)
	}
	got := unwrapData(t, []byte(out))
	if got["logged_in"] != true || got["name"] != "张三" || got["open_id"] != "ou_1" ||
		got["user_id"] != "u_1" || got["app_id"] != "cli_test" {
		t.Errorf("data 不符: %v", got)
	}
	if got["auth_expire_at_ms"] == nil || got["token_path"] == "" {
		t.Errorf("应含有效期与凭证路径: %v", got)
	}
}

func TestWhoamiAuthCeilingExpired(t *testing.T) {
	a := newAgentTestApp(t, nil, "")
	token := authTestToken(a)
	token.AuthExpireAt = a.now().Add(-time.Hour).UnixMilli()
	a.tokenStore = &fakeTokenStore{token: token}

	_, err := execAppCmd(t, a, newWhoamiCmd)
	if err == nil {
		t.Fatal("授权硬顶已过应报错")
	}
	if got := output.ExitCode(err); got != output.ExitAuth {
		t.Errorf("退出码 = %d, want %d", got, output.ExitAuth)
	}
}

func TestWhoamiRefreshExpired(t *testing.T) {
	a := newAgentTestApp(t, nil, "")
	token := authTestToken(a)
	token.RefreshExpireAt = a.now().Add(-time.Hour).UnixMilli()
	a.tokenStore = &fakeTokenStore{token: token}

	_, err := execAppCmd(t, a, newWhoamiCmd)
	if err == nil {
		t.Fatal("刷新凭证过期应报错")
	}
	if e := output.Classify(err); e.Type != output.TypeAuth {
		t.Errorf("error.type = %s, want auth", e.Type)
	}
}

func TestWhoamiToleratesOldTokenMissingFields(t *testing.T) {
	// 旧版凭证可能缺 AuthExpireAt/Name/Scope，whoami 应容忍而非报错
	a := newAgentTestApp(t, nil, "")
	token := authTestToken(a)
	token.AuthExpireAt, token.Name, token.Scope, token.UserID = 0, "", "", ""
	a.tokenStore = &fakeTokenStore{token: token}

	out, err := execAppCmd(t, a, newWhoamiCmd)
	if err != nil {
		t.Fatalf("旧版凭证应容忍缺字段: %v", err)
	}
	if !strings.Contains(out, "ou_1") {
		t.Errorf("人类输出应含 OpenID: %q", out)
	}
}

func TestWhoamiHuman(t *testing.T) {
	a := newAgentTestApp(t, nil, "")
	a.tokenStore = &fakeTokenStore{token: authTestToken(a)}

	out, err := execAppCmd(t, a, newWhoamiCmd)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"张三", "ou_1", "授权硬顶", "剩"} {
		if !strings.Contains(out, want) {
			t.Errorf("人类输出缺 %q: %q", want, out)
		}
	}
}

func TestLogoutNotLoggedInIdempotent(t *testing.T) {
	a := newAgentTestApp(t, nil, "")
	a.tokenStore = &fakeTokenStore{}
	a.jsonOut = true

	out, err := execAppCmd(t, a, newLogoutCmd)
	if err != nil {
		t.Fatalf("未登录 logout 应幂等成功: %v", err)
	}
	if got := unwrapData(t, []byte(out)); got["status"] != "not_logged_in" {
		t.Errorf("status = %v, want not_logged_in", got["status"])
	}
}

func TestLogoutRevokesAndDeletes(t *testing.T) {
	a := newAgentTestApp(t, nil, "")
	store := &fakeTokenStore{token: authTestToken(a)}
	revoker := &fakeTokenRevoker{}
	a.tokenStore = store
	a.tokenRevoker = revoker
	a.jsonOut = true

	out, err := execAppCmd(t, a, newLogoutCmd)
	if err != nil {
		t.Fatal(err)
	}
	got := unwrapData(t, []byte(out))
	if got["status"] != "logged_out" || got["remote_revoked"] != true {
		t.Errorf("data 不符: %v", got)
	}
	if revoker.calls == 0 {
		t.Error("应尝试远端撤销")
	}
	if store.token != nil {
		t.Error("本地凭证应被删除")
	}
}

func TestLogoutWithoutAppCredsStillDeletesLocal(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "")
	t.Setenv("FEISHU_APP_SECRET", "")
	a := newAgentTestApp(t, nil, "")
	store := &fakeTokenStore{token: authTestToken(a)}
	a.tokenStore = store
	a.jsonOut = true

	out, err := execAppCmd(t, a, newLogoutCmd)
	if err != nil {
		t.Fatal(err)
	}
	got := unwrapData(t, []byte(out))
	if got["status"] != "logged_out" || got["remote_revoked"] != false {
		t.Errorf("缺应用凭证应跳过远端撤销仍删本地: %v", got)
	}
	if store.token != nil {
		t.Error("本地凭证应被删除")
	}
}
