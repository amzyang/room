package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/amzyang/room/feishu"
	"github.com/amzyang/room/output"
)

// fakeRefreshTokenClient 只实现 RefreshUserToken 的 TokenClient（其余方法测试不触达）。
type fakeRefreshTokenClient struct {
	feishu.TokenClient
	result *feishu.UserTokenResult
	err    error
	calls  int
}

func (f *fakeRefreshTokenClient) RefreshUserToken(context.Context, string) (*feishu.UserTokenResult, error) {
	f.calls++
	return f.result, f.err
}

// rolledResult 保活成功的刷新响应：refresh 窗口滚动 7 天。
func rolledResult() *feishu.UserTokenResult {
	return &feishu.UserTokenResult{
		AccessToken: "new-at", AccessExpiresInSec: 7200,
		RefreshToken: "new-rt", RefreshExpiresInSec: 7 * 24 * 3600,
	}
}

func TestKeepaliveNotLoggedIn(t *testing.T) {
	a := newAgentTestApp(t, nil, "")
	a.tokenStore = &fakeTokenStore{}
	_, err := execAppCmd(t, a, newKeepaliveCmd)
	if err == nil {
		t.Fatal("未登录应报错")
	}
	e := output.Classify(err)
	if e.Type != output.TypeAuth || !strings.Contains(e.Hint, "room login") {
		t.Errorf("error = %+v, want auth + hint room login", e)
	}
}

func TestKeepaliveAuthCeilingExpired(t *testing.T) {
	a := newAgentTestApp(t, nil, "")
	token := authTestToken(a)
	token.AuthExpireAt = a.now().Add(-time.Hour).UnixMilli()
	a.tokenStore = &fakeTokenStore{token: token}

	_, err := execAppCmd(t, a, newKeepaliveCmd)
	if err == nil {
		t.Fatal("授权硬顶已过应报错")
	}
	if e := output.Classify(err); e.Type != output.TypeAuth {
		t.Errorf("error.type = %s, want auth", e.Type)
	}
}

func TestKeepaliveRefreshWindowExpired(t *testing.T) {
	a := newAgentTestApp(t, nil, "")
	token := authTestToken(a)
	token.RefreshExpireAt = a.now().Add(-time.Hour).UnixMilli()
	a.tokenStore = &fakeTokenStore{token: token}

	_, err := execAppCmd(t, a, newKeepaliveCmd)
	if err == nil {
		t.Fatal("刷新窗口已过应报错")
	}
	if e := output.Classify(err); e.Type != output.TypeAuth {
		t.Errorf("error.type = %s, want auth", e.Type)
	}
}

func TestKeepaliveMissingAppCredentials(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "")
	t.Setenv("FEISHU_APP_SECRET", "")
	a := newAgentTestApp(t, nil, "")
	a.tokenStore = &fakeTokenStore{token: authTestToken(a)}

	_, err := execAppCmd(t, a, newKeepaliveCmd)
	if err == nil {
		t.Fatal("缺应用凭证应报错")
	}
	if e := output.Classify(err); e.Type != output.TypeConfig {
		t.Errorf("error.type = %s, want config", e.Type)
	}
}

func TestKeepaliveOK(t *testing.T) {
	a := newAgentTestApp(t, nil, "")
	store := &fakeTokenStore{token: authTestToken(a)}
	a.tokenStore = store
	client := &fakeRefreshTokenClient{result: rolledResult()}
	a.tokenClient = client

	out, err := execAppCmd(t, a, newKeepaliveCmd)
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 {
		t.Errorf("refreshCalls = %d, want 1（access 有效也要刷）", client.calls)
	}
	if store.token.RefreshToken != "new-rt" {
		t.Errorf("刷新结果未持久化: %+v", store.token)
	}
	if !strings.Contains(out, "已刷新") {
		t.Errorf("输出应确认刷新: %q", out)
	}
}

func TestKeepaliveJSON(t *testing.T) {
	a := newAgentTestApp(t, nil, "")
	a.tokenStore = &fakeTokenStore{token: authTestToken(a)}
	a.tokenClient = &fakeRefreshTokenClient{result: rolledResult()}
	a.jsonOut = true

	out, err := execAppCmd(t, a, newKeepaliveCmd)
	if err != nil {
		t.Fatal(err)
	}
	got := unwrapData(t, []byte(out))
	if got["event"] != "keepalive_ok" {
		t.Errorf("event = %v, want keepalive_ok", got["event"])
	}
	wantMs := float64(a.now().Add(7 * 24 * time.Hour).UnixMilli())
	if got["refresh_expire_at_ms"] != wantMs {
		t.Errorf("refresh_expire_at_ms = %v, want %v", got["refresh_expire_at_ms"], wantMs)
	}
}

func TestKeepaliveRefreshFailureKeepsToken(t *testing.T) {
	a := newAgentTestApp(t, nil, "")
	orig := authTestToken(a)
	store := &fakeTokenStore{token: orig}
	a.tokenStore = store
	a.tokenClient = &fakeRefreshTokenClient{err: errors.New("http 400")}

	_, err := execAppCmd(t, a, newKeepaliveCmd)
	if err == nil {
		t.Fatal("刷新失败应报错")
	}
	e := output.Classify(err)
	if e.Type != output.TypeAPI || !e.Retryable {
		t.Errorf("error = %+v, want api + retryable", e)
	}
	if store.token != orig {
		t.Errorf("刷新失败不得改动本地凭证: %+v", store.token)
	}
}

func TestKeepaliveWarnsNearAuthExpiry(t *testing.T) {
	a := newAgentTestApp(t, nil, "")
	token := authTestToken(a)
	token.AuthExpireAt = a.now().Add(10 * 24 * time.Hour).UnixMilli() // 阈值 14 天内
	a.tokenStore = &fakeTokenStore{token: token}
	a.tokenClient = &fakeRefreshTokenClient{result: rolledResult()}
	a.jsonOut = true

	out, err := execAppCmd(t, a, newKeepaliveCmd)
	if err != nil {
		t.Fatal(err)
	}
	if got := unwrapData(t, []byte(out)); got["auth_near_expiry"] != true {
		t.Errorf("授权硬顶临近应标记 auth_near_expiry: %v", got)
	}
}
