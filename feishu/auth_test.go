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

	userInfoResult *UserIdentity
	userInfoErr    error
	userInfoCalls  int
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
func (f *fakeTokenClient) GetUserInfo(context.Context, string) (*UserIdentity, error) {
	f.userInfoCalls++
	if f.userInfoResult == nil && f.userInfoErr == nil {
		return &UserIdentity{}, nil
	}
	return f.userInfoResult, f.userInfoErr
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

func identityToken() *StoredUserToken {
	token := validToken()
	token.OpenID = "ou_old"
	token.UserID = "u_old"
	token.Name = "旧账号"
	return token
}

func TestRefreshIdentityOverwrites(t *testing.T) {
	store := &memoryStore{token: identityToken()}
	client := &fakeTokenClient{userInfoResult: &UserIdentity{OpenID: "ou_new", UserID: "u_new", Name: "新账号"}}
	auth := newTestAuth(AuthModeAuto, store, client, nowMs)

	got, err := auth.RefreshIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.OpenID != "ou_new" || got.UserID != "u_new" || got.Name != "新账号" {
		t.Errorf("identity 未覆盖: %+v", got)
	}
	if store.token.UserID != "u_new" {
		t.Errorf("identity 未持久化: %+v", store.token)
	}
	if store.token.AccessToken != "valid-access" {
		t.Errorf("token 字段被破坏: %+v", store.token)
	}
}

func TestRefreshIdentityFailureClears(t *testing.T) {
	store := &memoryStore{token: identityToken()}
	client := &fakeTokenClient{userInfoErr: errors.New("boom")}
	auth := newTestAuth(AuthModeAuto, store, client, nowMs)

	if _, err := auth.RefreshIdentity(context.Background()); err == nil {
		t.Fatal("want error")
	}
	if store.token.OpenID != "" || store.token.UserID != "" || store.token.Name != "" {
		t.Errorf("失败应清空旧身份防止换账号残留: %+v", store.token)
	}
	if store.token.AccessToken != "valid-access" {
		t.Errorf("token 字段被破坏: %+v", store.token)
	}
}

func TestUserIdentityFromStore(t *testing.T) {
	client := &fakeTokenClient{}
	auth := newTestAuth(AuthModeAuto, &memoryStore{token: identityToken()}, client, nowMs)

	got := auth.UserIdentity(context.Background())
	if got == nil || got.UserID != "u_old" || got.Name != "旧账号" {
		t.Errorf("identity = %+v, want 存储中的身份", got)
	}
	if client.userInfoCalls != 0 {
		t.Error("存储已有身份不应发起网络请求")
	}
}

func TestUserIdentityLazyBackfill(t *testing.T) {
	store := &memoryStore{token: validToken()}
	client := &fakeTokenClient{userInfoResult: &UserIdentity{OpenID: "ou_1", UserID: "u_1", Name: "张三"}}
	auth := newTestAuth(AuthModeAuto, store, client, nowMs)

	got := auth.UserIdentity(context.Background())
	if got == nil || got.UserID != "u_1" {
		t.Fatalf("identity = %+v, want backfill 结果", got)
	}
	if store.token.UserID != "u_1" || store.token.OpenID != "ou_1" {
		t.Errorf("backfill 应回写存储: %+v", store.token)
	}
	if client.userInfoCalls != 1 {
		t.Errorf("userInfoCalls = %d, want 1", client.userInfoCalls)
	}
}

func TestUserIdentityBackfillAfterRefreshKeepsNewToken(t *testing.T) {
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
		userInfoResult: &UserIdentity{OpenID: "ou_1", UserID: "u_1", Name: "张三"},
	}
	auth := newTestAuth(AuthModeAuto, store, client, nowMs)

	got := auth.UserIdentity(context.Background())
	if got == nil || got.UserID != "u_1" {
		t.Fatalf("identity = %+v, want backfill 结果", got)
	}
	if store.token.AccessToken != "new-access" {
		t.Errorf("回写不得冲掉刚刷新的 access_token: %+v", store.token)
	}
	if store.token.UserID != "u_1" {
		t.Errorf("identity 未回写: %+v", store.token)
	}
}

func TestUserIdentityBackfillFailure(t *testing.T) {
	client := &fakeTokenClient{userInfoErr: errors.New("boom")}
	auth := newTestAuth(AuthModeAuto, &memoryStore{token: validToken()}, client, nowMs)

	if got := auth.UserIdentity(context.Background()); got != nil {
		t.Errorf("backfill 失败应返回 nil, got %+v", got)
	}
}

func TestUserIdentityNoToken(t *testing.T) {
	client := &fakeTokenClient{}
	auth := newTestAuth(AuthModeAuto, &memoryStore{}, client, nowMs)

	if got := auth.UserIdentity(context.Background()); got != nil {
		t.Errorf("无凭证应返回 nil, got %+v", got)
	}
	if client.userInfoCalls != 0 {
		t.Error("无凭证不应发起网络请求")
	}
}

func TestRefreshPreservesIdentity(t *testing.T) {
	expired := identityToken()
	expired.AccessExpireAt = nowMs - 1000
	store := &memoryStore{token: expired}
	client := &fakeTokenClient{
		refreshResult: &UserTokenResult{AccessToken: "new-access", AccessExpiresInSec: 7200,
			RefreshToken: "new-refresh", RefreshExpiresInSec: 30 * 24 * 3600},
	}
	auth := newTestAuth(AuthModeAuto, store, client, nowMs)

	if got := auth.UserAccessToken(context.Background()); got != "new-access" {
		t.Fatalf("got %q, want new-access", got)
	}
	if store.token.OpenID != "ou_old" || store.token.UserID != "u_old" || store.token.Name != "旧账号" {
		t.Errorf("刷新后身份字段丢失: %+v", store.token)
	}
}

func TestForceRefreshRollsWindowEvenWhenAccessValid(t *testing.T) {
	store := &memoryStore{token: validToken()} // access 仍有效，保活也要刷
	client := &fakeTokenClient{
		refreshResult: &UserTokenResult{
			AccessToken: "new-access", AccessExpiresInSec: 7200,
			RefreshToken: "new-refresh", RefreshExpiresInSec: 7 * 24 * 3600,
		},
	}
	auth := newTestAuth(AuthModeAuto, store, client, nowMs)

	stored, err := auth.ForceRefresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if client.refreshCalls != 1 {
		t.Errorf("refreshCalls = %d, want 1", client.refreshCalls)
	}
	if stored.RefreshToken != "new-refresh" || store.token.RefreshToken != "new-refresh" {
		t.Errorf("刷新结果未持久化: %+v", store.token)
	}
	if stored.RefreshExpireAt != nowMs+7*24*3600_000 {
		t.Errorf("RefreshExpireAt = %d, 窗口未滚动", stored.RefreshExpireAt)
	}
}

func TestForceRefreshFailureKeepsStore(t *testing.T) {
	orig := validToken()
	store := &memoryStore{token: orig}
	client := &fakeTokenClient{refreshErr: errors.New("http 400")}
	auth := newTestAuth(AuthModeAuto, store, client, nowMs)

	if _, err := auth.ForceRefresh(context.Background()); err == nil {
		t.Fatal("刷新失败应返回错误")
	}
	if store.token != orig {
		t.Errorf("刷新失败不得改动本地凭证: %+v", store.token)
	}
}

func TestPersistKeepsRefreshFieldsWhenResponseOmitsThem(t *testing.T) {
	store := &memoryStore{}
	auth := newTestAuth(AuthModeAuto, store, &fakeTokenClient{}, nowMs)
	prev := &StoredUserToken{
		RefreshToken:    "old-refresh",
		RefreshExpireAt: nowMs + 86400_000,
	}

	stored := auth.Persist(&UserTokenResult{AccessToken: "new-access", AccessExpiresInSec: 7200}, prev)

	if stored.RefreshToken != "old-refresh" {
		t.Errorf("RefreshToken = %q, want old-refresh", stored.RefreshToken)
	}
	if stored.RefreshExpireAt != nowMs+86400_000 {
		t.Errorf("RefreshExpireAt = %d, want %d(保留 prev)", stored.RefreshExpireAt, nowMs+86400_000)
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
