package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sentry "github.com/getsentry/sentry-go"

	"github.com/amzyang/room/config"
	"github.com/amzyang/room/output"
)

// fakeSentryTransport 内存 Transport：记录发出的事件，flush 结果可配。
type fakeSentryTransport struct {
	events  []*sentry.Event
	flushOK bool
}

func (f *fakeSentryTransport) Configure(sentry.ClientOptions)        {}
func (f *fakeSentryTransport) SendEvent(e *sentry.Event)             { f.events = append(f.events, e) }
func (f *fakeSentryTransport) Flush(time.Duration) bool              { return f.flushOK }
func (f *fakeSentryTransport) FlushWithContext(context.Context) bool { return f.flushOK }
func (f *fakeSentryTransport) Close()                                {}

const sentryTestDSN = "https://abcdef1234567890@o1.ingest.sentry.io/42"

func TestResolveSentryDSNStatus(t *testing.T) {
	entryTOML := config.Entry{Value: "https://toml@h/1", Source: config.SourceTOML}
	entryShell := config.Entry{Value: "https://shell@h/1", Source: config.SourceShellEnv}
	cases := []struct {
		name      string
		flagValue string
		flagSet   bool
		entry     config.Entry
		builtin   string
		wantDSN   string
		wantSrc   sentryDSNSource
	}{
		{"flag 优先于 env 与内置", "https://flag@h/1", true, entryTOML, "https://b@h/1", "https://flag@h/1", sentrySourceFlag},
		{"flag 显式设空禁用", "", true, entryShell, "https://b@h/1", "", sentrySourceDisabled},
		{"shell 环境变量", "", false, entryShell, "", "https://shell@h/1", sentrySourceShellEnv},
		{"config.toml", "", false, entryTOML, "", "https://toml@h/1", sentrySourceTOML},
		{"env 层显式设空禁用", "", false, config.Entry{Value: "", Source: config.SourceShellEnv}, "https://b@h/1", "", sentrySourceDisabled},
		{"编译内置", "", false, config.Entry{}, "https://b@h/1", "https://b@h/1", sentrySourceBuiltin},
		{"全部未设置", "", false, config.Entry{}, "", "", sentrySourceUnset},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dsn, src := resolveSentryDSNStatus(c.flagValue, c.flagSet, c.entry, c.builtin)
			if dsn != c.wantDSN || src != c.wantSrc {
				t.Errorf("got (%q, %s), want (%q, %s)", dsn, src, c.wantDSN, c.wantSrc)
			}
		})
	}
}

func TestBuildSentryStatus(t *testing.T) {
	st := buildSentryStatus(sentryTestDSN, sentrySourceTOML, "v1.2.3")
	if !st.Configured || st.Host != "o1.ingest.sentry.io" || st.ProjectID != "42" {
		t.Errorf("DSN 剖析不符: %+v", st)
	}
	if st.PublicKey != "****7890" {
		t.Errorf("public key 应脱敏保留末 4 位: %q", st.PublicKey)
	}
	if st.Release != "v1.2.3" || st.DSNError != "" {
		t.Errorf("release/dsn_error 不符: %+v", st)
	}

	bad := buildSentryStatus("not-a-dsn", sentrySourceShellEnv, "dev")
	if bad.DSNError == "" || bad.Host != "" {
		t.Errorf("非法 DSN 应记录解析错误且不剖析: %+v", bad)
	}
	if !bad.Configured {
		t.Errorf("值非空即算已配置: %+v", bad)
	}

	empty := buildSentryStatus("", sentrySourceUnset, "dev")
	if empty.Configured {
		t.Errorf("空 DSN 不应算已配置: %+v", empty)
	}
}

func TestFormatSentryStatus(t *testing.T) {
	set := formatSentryStatus(buildSentryStatus(sentryTestDSN, sentrySourceTOML, "v1"), true)
	for _, want := range []string{"config.toml", "o1.ingest.sentry.io", "****7890", "已初始化"} {
		if !strings.Contains(set, want) {
			t.Errorf("已配置输出缺少 %q:\n%s", want, set)
		}
	}
	unset := formatSentryStatus(buildSentryStatus("", sentrySourceUnset, "dev"), false)
	if !strings.Contains(unset, "未配置") || !strings.Contains(unset, "room config set sentry.dsn") {
		t.Errorf("未配置输出应含来源与配置提示:\n%s", unset)
	}
	disabled := formatSentryStatus(buildSentryStatus("", sentrySourceDisabled, "dev"), false)
	if !strings.Contains(disabled, "已显式禁用") {
		t.Errorf("禁用输出不符:\n%s", disabled)
	}
}

func TestSentryUnsetShowsStatusExitZero(t *testing.T) {
	fake := &fakeSentryTransport{flushOK: true}
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	a.sentryTransport = fake

	out, err := execAppCmd(t, a, newSentryCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "未配置") {
		t.Errorf("应展示未配置状态: %s", out)
	}
	if len(fake.events) != 0 {
		t.Errorf("未配置不应发送事件, got %d", len(fake.events))
	}
}

func TestSentryUnsetJSON(t *testing.T) {
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	a.jsonOut = true

	out, err := execAppCmd(t, a, newSentryCmd)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Configured bool              `json:"configured"`
			Source     string            `json:"source"`
			Sent       *sentrySendResult `json:"sent"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("非法 JSON: %v: %s", err, out)
	}
	if !env.OK || env.Data.Configured || env.Data.Source != "unset" || env.Data.Sent != nil {
		t.Errorf("信封不符: %+v", env)
	}
}

func TestSentryConfiguredSendsViaFakeTransport(t *testing.T) {
	fake := &fakeSentryTransport{flushOK: true}
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	a.jsonOut = true
	a.sentryTransport = fake
	a.cfg.Entries["SENTRY_DSN"] = config.Entry{Value: sentryTestDSN, Source: config.SourceTOML}
	t.Cleanup(func() { sentry.CurrentHub().BindClient(nil) })

	out, err := execAppCmd(t, a, newSentryCmd)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Source     string            `json:"source"`
			Host       string            `json:"host"`
			ProjectID  string            `json:"project_id"`
			ClientInit bool              `json:"client_initialized"`
			Sent       *sentrySendResult `json:"sent"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("非法 JSON: %v: %s", err, out)
	}
	d := env.Data
	if !env.OK || d.Source != "config.toml" || d.Host != "o1.ingest.sentry.io" || d.ProjectID != "42" || !d.ClientInit {
		t.Errorf("状态字段不符: %+v", d)
	}
	if d.Sent == nil || !d.Sent.Flushed || len(d.Sent.ExceptionEventID) != 32 || len(d.Sent.MessageEventID) != 32 {
		t.Errorf("发送结果不符: %+v", d.Sent)
	}

	if len(fake.events) != 2 {
		t.Fatalf("应发送 2 个事件, got %d", len(fake.events))
	}
	var gotException, gotMessage bool
	for _, e := range fake.events {
		if e.Tags["e2e"] != "true" || e.Tags["command"] != "room-sentry" {
			t.Errorf("事件缺少 e2e tag: %+v", e.Tags)
		}
		if len(e.Exception) > 0 && strings.Contains(e.Exception[0].Type, "sentrySelftestError") {
			gotException = true
		}
		if strings.Contains(e.Message, "[room-selftest]") {
			gotMessage = true
		}
	}
	if !gotException || !gotMessage {
		t.Errorf("应各含一条 exception 与 message: exception=%v message=%v", gotException, gotMessage)
	}
}

func TestSentryInvalidDSNIsConfigError(t *testing.T) {
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	a.cfg.Entries["SENTRY_DSN"] = config.Entry{Value: "not-a-dsn", Source: config.SourceShellEnv}

	_, err := execAppCmd(t, a, newSentryCmd)
	if err == nil {
		t.Fatal("非法 DSN 应报错")
	}
	if e := output.Classify(err); e.Type != output.TypeConfig {
		t.Errorf("error.type = %s, want config", e.Type)
	}
	if got := output.ExitCode(err); got != output.ExitAuth {
		t.Errorf("退出码 = %d, want %d", got, output.ExitAuth)
	}
}

func TestSentryFlushTimeoutIsAPIError(t *testing.T) {
	fake := &fakeSentryTransport{flushOK: false}
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	a.sentryTransport = fake
	a.cfg.Entries["SENTRY_DSN"] = config.Entry{Value: sentryTestDSN, Source: config.SourceTOML}
	t.Cleanup(func() { sentry.CurrentHub().BindClient(nil) })

	_, err := execAppCmd(t, a, newSentryCmd)
	if err == nil {
		t.Fatal("flush 超时应报错")
	}
	if e := output.Classify(err); e.Type != output.TypeAPI {
		t.Errorf("error.type = %s, want api", e.Type)
	}
	if got := output.ExitCode(err); got != output.ExitAPI {
		t.Errorf("退出码 = %d, want %d", got, output.ExitAPI)
	}
}

func TestSentryDisabledViaRootFlag(t *testing.T) {
	// execRootCmd 固定前置 --sentry-dsn=，同时覆盖 flag 路径与命令树注册
	out, _, err := execRootCmd(t, "sentry")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "已显式禁用") {
		t.Errorf("应展示显式禁用状态: %s", out)
	}
}

func TestSentryCmdHidden(t *testing.T) {
	root, _ := newRootCmd(&config.Resolved{Entries: map[string]config.Entry{}})
	for _, c := range root.Commands() {
		if c.Name() == "sentry" {
			if !c.Hidden {
				t.Error("sentry 子命令应为 Hidden")
			}
			return
		}
	}
	t.Fatal("sentry 子命令未注册")
}
