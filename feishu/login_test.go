package feishu

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

// pollScriptClient 按脚本依次返回轮询结果。
type pollScriptClient struct {
	fakeTokenClient
	script []DevicePollResult
	calls  int
	err    error
}

func (p *pollScriptClient) PollDeviceToken(context.Context, string) (*DevicePollResult, error) {
	if p.err != nil {
		return nil, p.err
	}
	r := p.script[p.calls]
	p.calls++
	return &r, nil
}

// loginClock 每次读取前进 step，模拟轮询消耗真实时间。
func loginClock(start time.Time, step time.Duration) func() time.Time {
	cur := start
	return func() time.Time {
		cur = cur.Add(step)
		return cur
	}
}

func noSleep(intervals *[]time.Duration) func(context.Context, time.Duration) error {
	return func(_ context.Context, d time.Duration) error {
		*intervals = append(*intervals, d)
		return nil
	}
}

func TestPollDeviceLoginSuccess(t *testing.T) {
	client := &pollScriptClient{script: []DevicePollResult{
		{Status: PollPending},
		{Status: PollSuccess, Token: &UserTokenResult{AccessToken: "at", RefreshToken: "rt", AccessExpiresInSec: 3600}},
	}}
	store := &memoryStore{}
	auth := newTestAuth(AuthModeAuto, store, client, nowMs)

	var intervals []time.Duration
	got, err := pollDeviceLogin(context.Background(), auth, "dc", 5, 300, noSleep(&intervals))
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "at" {
		t.Errorf("AccessToken = %q", got.AccessToken)
	}
	if store.token == nil || store.token.AccessToken != "at" {
		t.Error("成功后应持久化凭证")
	}
	if len(intervals) != 2 || intervals[0] != 5*time.Second {
		t.Errorf("轮询间隔不符: %v", intervals)
	}
}

// recordingStore 记录每次写盘快照，用于断言中间持久化状态。
type recordingStore struct {
	memoryStore
	writes []StoredUserToken
}

func (r *recordingStore) Write(t *StoredUserToken) error {
	r.writes = append(r.writes, *t)
	return r.memoryStore.Write(t)
}

func TestPollDeviceLoginFirstWriteDropsPriorIdentity(t *testing.T) {
	client := &pollScriptClient{script: []DevicePollResult{
		{Status: PollSuccess, Token: &UserTokenResult{AccessToken: "at", RefreshToken: "rt", AccessExpiresInSec: 3600}},
	}}
	client.userInfoResult = &UserIdentity{OpenID: "ou_new", UserID: "u_new", Name: "新账号"}
	store := &recordingStore{memoryStore: memoryStore{token: identityToken()}}
	auth := newTestAuth(AuthModeAuto, store, client, nowMs)

	got, err := pollDeviceLogin(context.Background(), auth, "dc", 5, 300, noSleep(&[]time.Duration{}))
	if err != nil {
		t.Fatal(err)
	}
	// 首次写盘（token 持久化）不得携带上一账号身份：RefreshIdentity 写盘前
	// 进程中断时，宁可暂无身份（下次预订 lazy backfill）也不能换账号错配
	first := store.writes[0]
	if first.OpenID != "" || first.UserID != "" || first.Name != "" {
		t.Errorf("首次持久化残留上一账号身份: %+v", first)
	}
	if got.OpenID != "ou_new" || got.UserID != "u_new" {
		t.Errorf("最终身份应为新账号: %+v", got)
	}
}

func TestPollDeviceLoginPersistsIdentity(t *testing.T) {
	client := &pollScriptClient{script: []DevicePollResult{
		{Status: PollSuccess, Token: &UserTokenResult{AccessToken: "at", RefreshToken: "rt", AccessExpiresInSec: 3600}},
	}}
	client.userInfoResult = &UserIdentity{OpenID: "ou_1", UserID: "u_1", Name: "张三"}
	store := &memoryStore{}
	auth := newTestAuth(AuthModeAuto, store, client, nowMs)

	got, err := pollDeviceLogin(context.Background(), auth, "dc", 5, 300, noSleep(&[]time.Duration{}))
	if err != nil {
		t.Fatal(err)
	}
	if got.OpenID != "ou_1" || got.UserID != "u_1" || got.Name != "张三" {
		t.Errorf("返回的凭证应带身份: %+v", got)
	}
	if store.token.UserID != "u_1" {
		t.Errorf("身份未持久化: %+v", store.token)
	}
}

func TestPollDeviceLoginUserInfoFailureStillSucceeds(t *testing.T) {
	client := &pollScriptClient{script: []DevicePollResult{
		{Status: PollSuccess, Token: &UserTokenResult{AccessToken: "at", RefreshToken: "rt", AccessExpiresInSec: 3600}},
	}}
	client.userInfoErr = errors.New("permission denied")
	store := &memoryStore{}
	auth := newTestAuth(AuthModeAuto, store, client, nowMs)

	got, err := pollDeviceLogin(context.Background(), auth, "dc", 5, 300, noSleep(&[]time.Duration{}))
	if err != nil {
		t.Fatalf("user_info 失败不应阻断登录: %v", err)
	}
	if got.AccessToken != "at" || store.token == nil || store.token.AccessToken != "at" {
		t.Errorf("凭证应正常持久化: %+v", store.token)
	}
	if got.OpenID != "" || got.UserID != "" {
		t.Errorf("身份应为空: %+v", got)
	}
}

func TestPollDeviceLoginSlowDown(t *testing.T) {
	client := &pollScriptClient{script: []DevicePollResult{
		{Status: PollSlowDown},
		{Status: PollSuccess, Token: &UserTokenResult{AccessToken: "at"}},
	}}
	auth := newTestAuth(AuthModeAuto, &memoryStore{}, client, nowMs)

	var intervals []time.Duration
	if _, err := pollDeviceLogin(context.Background(), auth, "dc", 5, 300, noSleep(&intervals)); err != nil {
		t.Fatal(err)
	}
	if intervals[1] != 10*time.Second {
		t.Errorf("slow_down 后间隔应 +5s: %v", intervals)
	}
}

func TestPollDeviceLoginExpired(t *testing.T) {
	client := &pollScriptClient{script: []DevicePollResult{{Status: PollPending}, {Status: PollPending}}}
	auth := &Auth{
		TokenClient: client,
		Store:       &memoryStore{},
		Clock:       loginClock(time.UnixMilli(nowMs), 40*time.Second),
		Log:         slog.New(slog.DiscardHandler),
	}

	var intervals []time.Duration
	_, err := pollDeviceLogin(context.Background(), auth, "dc", 5, 60, noSleep(&intervals))
	if !errors.Is(err, ErrLoginExpired) {
		t.Errorf("超时应返回 ErrLoginExpired: %v", err)
	}
}

func TestPollDeviceLoginPollError(t *testing.T) {
	boom := errors.New("access_denied")
	client := &pollScriptClient{err: boom}
	auth := newTestAuth(AuthModeAuto, &memoryStore{}, client, nowMs)

	if _, err := pollDeviceLogin(context.Background(), auth, "dc", 5, 300, noSleep(&[]time.Duration{})); !errors.Is(err, boom) {
		t.Errorf("轮询错误应向上传递: %v", err)
	}
}
