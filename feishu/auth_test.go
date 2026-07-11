package feishu

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

type memoryStore struct {
	token *StoredUserToken
}

func (m *memoryStore) Read() *StoredUserToken { return m.token }
func (m *memoryStore) Write(t *StoredUserToken) error {
	m.token = t
	return nil
}

type fakeTokenClient struct {
	refreshResult *UserTokenResult
	refreshErr    error
	refreshCalls  int
}

func (f *fakeTokenClient) VerifyTenantCredentials(context.Context) error { return nil }
func (f *fakeTokenClient) RequestDeviceAuthorization(context.Context, string) (*DeviceAuthorization, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeTokenClient) PollDeviceToken(context.Context, string) (*DevicePollResult, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeTokenClient) RefreshUserToken(context.Context, string) (*UserTokenResult, error) {
	f.refreshCalls++
	return f.refreshResult, f.refreshErr
}

func newTestAuth(mode AuthMode, store UserTokenStore, client TokenClient, nowMs int64) *Auth {
	return &Auth{
		Mode:        mode,
		TokenClient: client,
		Store:       store,
		Clock:       func() time.Time { return time.UnixMilli(nowMs) },
		Log:         slog.New(slog.DiscardHandler),
	}
}

const nowMs = int64(1_700_000_000_000)

func validToken() *StoredUserToken {
	return &StoredUserToken{
		AccessToken:     "valid-access",
		AccessExpireAt:  nowMs + 3600_000,
		RefreshToken:    "valid-refresh",
		RefreshExpireAt: nowMs + 86400_000,
	}
}

func TestUserAccessTokenValid(t *testing.T) {
	client := &fakeTokenClient{}
	auth := newTestAuth(AuthModeAuto, &memoryStore{token: validToken()}, client, nowMs)

	if got := auth.UserAccessToken(context.Background()); got != "valid-access" {
		t.Errorf("got %q, want valid-access", got)
	}
	if client.refreshCalls != 0 {
		t.Error("valid access token should not trigger refresh")
	}
}

func TestUserAccessTokenRefreshAndPersist(t *testing.T) {
	expired := validToken()
	expired.AccessExpireAt = nowMs - 1000
	store := &memoryStore{token: expired}
	client := &fakeTokenClient{
		refreshResult: &UserTokenResult{
			AccessToken:         "new-access",
			AccessExpiresInSec:  7200,
			RefreshToken:        "new-refresh",
			RefreshExpiresInSec: 30 * 24 * 3600,
		},
	}
	auth := newTestAuth(AuthModeAuto, store, client, nowMs)

	if got := auth.UserAccessToken(context.Background()); got != "new-access" {
		t.Errorf("got %q, want new-access", got)
	}
	if store.token.AccessToken != "new-access" || store.token.RefreshToken != "new-refresh" {
		t.Errorf("refreshed token not persisted: %+v", store.token)
	}
	if store.token.AccessExpireAt != nowMs+7200_000 {
		t.Errorf("accessExpireAt = %d, want %d", store.token.AccessExpireAt, nowMs+7200_000)
	}
}

func TestUserAccessTokenRefreshFailureFallsBack(t *testing.T) {
	expired := validToken()
	expired.AccessExpireAt = nowMs - 1000
	client := &fakeTokenClient{refreshErr: errors.New("boom")}
	auth := newTestAuth(AuthModeAuto, &memoryStore{token: expired}, client, nowMs)

	if got := auth.UserAccessToken(context.Background()); got != "" {
		t.Errorf("refresh failure should return empty, got %q", got)
	}
}

func TestUserAccessTokenRefreshExpired(t *testing.T) {
	dead := validToken()
	dead.AccessExpireAt = nowMs - 1000
	dead.RefreshExpireAt = nowMs - 1000
	client := &fakeTokenClient{}
	auth := newTestAuth(AuthModeAuto, &memoryStore{token: dead}, client, nowMs)

	if got := auth.UserAccessToken(context.Background()); got != "" {
		t.Errorf("dead refresh token should return empty, got %q", got)
	}
	if client.refreshCalls != 0 {
		t.Error("dead refresh token should not attempt refresh")
	}
}

func TestCalendarAuthOptions(t *testing.T) {
	// auto + 有效用户凭证 → 用户身份（一个 option）
	auth := newTestAuth(AuthModeAuto, &memoryStore{token: validToken()}, &fakeTokenClient{}, nowMs)
	opts, err := auth.CalendarAuthOptions(context.Background())
	if err != nil || len(opts) != 1 {
		t.Errorf("auto with valid token: opts=%d err=%v, want 1 option", len(opts), err)
	}

	// auto + 无凭证 → 应用身份（空 options）
	auth = newTestAuth(AuthModeAuto, &memoryStore{}, &fakeTokenClient{}, nowMs)
	opts, err = auth.CalendarAuthOptions(context.Background())
	if err != nil || len(opts) != 0 {
		t.Errorf("auto without token: opts=%d err=%v, want 0 options", len(opts), err)
	}

	// user + 无凭证 → 报错
	auth = newTestAuth(AuthModeUser, &memoryStore{}, &fakeTokenClient{}, nowMs)
	if _, err = auth.CalendarAuthOptions(context.Background()); err == nil {
		t.Error("user mode without token should error")
	}

	// tenant → 不触用户凭证，应用身份
	client := &fakeTokenClient{}
	auth = newTestAuth(AuthModeTenant, &memoryStore{token: validToken()}, client, nowMs)
	opts, err = auth.CalendarAuthOptions(context.Background())
	if err != nil || len(opts) != 0 {
		t.Errorf("tenant mode: opts=%d err=%v, want 0 options", len(opts), err)
	}
}

// 进程内一次决策：第一次决策后即使凭证状态变化也不再改变身份。
func TestCalendarAuthOptionsDecidedOnce(t *testing.T) {
	store := &memoryStore{}
	auth := newTestAuth(AuthModeAuto, store, &fakeTokenClient{}, nowMs)

	opts, _ := auth.CalendarAuthOptions(context.Background())
	if len(opts) != 0 {
		t.Fatal("first decision should be tenant fallback")
	}

	store.token = validToken()
	opts, _ = auth.CalendarAuthOptions(context.Background())
	if len(opts) != 0 {
		t.Error("decision should be cached per process, not re-evaluated")
	}
}
