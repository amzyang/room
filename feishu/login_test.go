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
