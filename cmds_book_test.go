package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/amzyang/room/booking"
	"github.com/amzyang/room/config"
	"github.com/amzyang/room/logx"
	"github.com/amzyang/room/output"
)

// fakeBookingSvc 内存 bookingService，记录调用参数。
type fakeBookingSvc struct {
	result      *booking.BookResult
	bookErr     error
	events      []booking.EventSummary
	listErr     error
	outcome     *booking.CancelOutcome
	cancelErr   error
	autoResults []booking.BookResult
	autoErr     error

	gotDate, gotStart, gotEnd, gotTitle string
	gotParticipants                     []string
	gotCancelID                         string
	gotAutoDryRun                       bool
	gotFrom, gotTo                      time.Time
	gotMine                             bool
}

func (f *fakeBookingSvc) AutoBook(_ context.Context, dryRun bool) ([]booking.BookResult, error) {
	f.gotAutoDryRun = dryRun
	return f.autoResults, f.autoErr
}

func (f *fakeBookingSvc) ListEvents(_ context.Context, from, to time.Time, organizedByMeOnly bool) ([]booking.EventSummary, error) {
	f.gotFrom, f.gotTo, f.gotMine = from, to, organizedByMeOnly
	return f.events, f.listErr
}

func (f *fakeBookingSvc) CancelEvent(_ context.Context, eventID string) (*booking.CancelOutcome, error) {
	f.gotCancelID = eventID
	return f.outcome, f.cancelErr
}

func (f *fakeBookingSvc) BookRoom(_ context.Context, date, startTime, endTime, title string, participants []string) (*booking.BookResult, error) {
	f.gotDate, f.gotStart, f.gotEnd, f.gotTitle = date, startTime, endTime, title
	f.gotParticipants = participants
	return f.result, f.bookErr
}

// newAgentTestApp 非交互（非 TTY）环境下的测试 app，stdin 输入可注入。
func newAgentTestApp(t *testing.T, svc bookingService, stdin string) *app {
	t.Helper()
	loc, _ := time.LoadLocation("Asia/Shanghai")
	a := &app{
		loc: loc,
		log: logx.New(false, loc),
		now: func() time.Time { return time.Date(2026, 7, 10, 9, 0, 0, 0, loc) },
		cfg: &config.Resolved{Entries: map[string]config.Entry{}},
		streams: &output.Streams{
			In: strings.NewReader(stdin), Out: io.Discard, Err: io.Discard,
		},
	}
	a.newService = func(context.Context, bool) (bookingService, error) { return svc, nil }
	return a
}

func execAppCmd(t *testing.T, a *app, newCmd func(*app) *cobra.Command, args ...string) (string, error) {
	t.Helper()
	cmd := newCmd(a)
	// 生产环境由 root 命令统一静默 usage/错误输出，单测直接执行子命令需自行设置
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func bookedResult() *booking.BookResult {
	return &booking.BookResult{
		Status:  booking.StatusBooked,
		EventID: "evt_123",
		Room:    &booking.BookedRoom{ID: "omm_1", Name: "3F-A"},
		Date:    "2026-07-15", StartTime: "14:00:00", EndTime: "15:00:00",
		Title: "meeting", ParticipantsResolved: 0,
	}
}

func TestBookNonInteractiveMissingInputFailsFast(t *testing.T) {
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	_, err := execAppCmd(t, a, newBookCmd)
	if err == nil {
		t.Fatal("非交互缺输入应 fail-fast 而非挂起")
	}
	if got := output.ExitCode(err); got != output.ExitValidation {
		t.Errorf("退出码 = %d, want %d", got, output.ExitValidation)
	}
	if hint := output.Classify(err).Hint; !strings.Contains(hint, "-d") || !strings.Contains(hint, "-t") {
		t.Errorf("hint 应指引显式参数: %q", hint)
	}
}

func TestBookNonInteractiveMissingTimeFailsFast(t *testing.T) {
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	_, err := execAppCmd(t, a, newBookCmd, "-d", "07-15")
	if err == nil || output.ExitCode(err) != output.ExitValidation {
		t.Fatalf("缺 -t 应归 validation: %v", err)
	}
}

func TestBookBookedJSON(t *testing.T) {
	svc := &fakeBookingSvc{result: bookedResult()}
	a := newAgentTestApp(t, svc, "")
	a.jsonOut = true

	out, err := execAppCmd(t, a, newBookCmd, "-d", "07-15", "-t", "14:00-15:00")
	if err != nil {
		t.Fatal(err)
	}
	got := unwrapData(t, []byte(out))
	if got["status"] != "booked" || got["event_id"] != "evt_123" {
		t.Errorf("data 不符: %v", got)
	}
	room := got["room"].(map[string]any)
	if room["id"] != "omm_1" || room["name"] != "3F-A" {
		t.Errorf("room 不符: %v", room)
	}
	if svc.gotDate != "2026-07-15" || svc.gotStart != "14:00:00" || svc.gotEnd != "15:00:00" {
		t.Errorf("服务调用参数不符: %s %s-%s", svc.gotDate, svc.gotStart, svc.gotEnd)
	}
}

func TestBookBookedHumanStdout(t *testing.T) {
	a := newAgentTestApp(t, &fakeBookingSvc{result: bookedResult()}, "")
	out, err := execAppCmd(t, a, newBookCmd, "-d", "07-15", "-t", "14:00-15:00")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "已预订") || !strings.Contains(out, "evt_123") || !strings.Contains(out, "3F-A") {
		t.Errorf("人类模式成功结果应打到 stdout: %q", out)
	}
}

func TestBookNotBookedIsTypedError(t *testing.T) {
	tests := []struct {
		status   booking.BookStatus
		wantType output.ErrType
	}{
		{booking.StatusNoRoom, output.TypeNoRoom},
		{booking.StatusConflict, output.TypeConflict},
		{booking.StatusHolidaySkipped, output.TypeHolidaySkipped},
		{booking.StatusNoParticipants, output.TypeNoParticipants},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			svc := &fakeBookingSvc{result: &booking.BookResult{
				Status: tt.status, Date: "2026-07-15", StartTime: "14:00:00", EndTime: "15:00:00",
			}}
			a := newAgentTestApp(t, svc, "")
			out, err := execAppCmd(t, a, newBookCmd, "-d", "07-15", "-t", "14:00-15:00")
			if err == nil {
				t.Fatal("未订到应返回错误（exit 0 ⟺ 订上）")
			}
			e := output.Classify(err)
			if e.Type != tt.wantType {
				t.Errorf("error.type = %s, want %s", e.Type, tt.wantType)
			}
			if output.ExitCode(err) != output.ExitAPI {
				t.Errorf("退出码 = %d, want 1", output.ExitCode(err))
			}
			if out != "" {
				t.Errorf("未订到时 stdout 应为空: %q", out)
			}
		})
	}
}

func TestBookNoParticipantsHintAndDetail(t *testing.T) {
	svc := &fakeBookingSvc{result: &booking.BookResult{
		Status: booking.StatusNoParticipants,
		Date:   "2026-07-15", StartTime: "14:00:00", EndTime: "15:00:00",
		ParticipantsUnresolved: []string{"ghost"},
	}}
	a := newAgentTestApp(t, svc, "")
	_, err := execAppCmd(t, a, newBookCmd, "-d", "07-15", "-t", "14:00-15:00")
	if err == nil {
		t.Fatal("无有效参会人应返回错误")
	}
	e := output.Classify(err)
	if e.Type != output.TypeNoParticipants {
		t.Errorf("error.type = %s, want no_participants", e.Type)
	}
	if !strings.Contains(e.Hint, "room login") {
		t.Errorf("hint 应指引 room login: %q", e.Hint)
	}
	detail, ok := e.Detail.(map[string]any)
	if !ok {
		t.Fatalf("detail 类型不符: %T", e.Detail)
	}
	unresolved, _ := detail["participants_unresolved"].([]string)
	if len(unresolved) != 1 || unresolved[0] != "ghost" {
		t.Errorf("detail.participants_unresolved = %v, want [ghost]", detail["participants_unresolved"])
	}
}

func TestBookTitleFlagWins(t *testing.T) {
	svc := &fakeBookingSvc{result: bookedResult()}
	a := newAgentTestApp(t, svc, "")
	if _, err := execAppCmd(t, a, newBookCmd, "-d", "07-15", "-t", "14:00-15:00", "--title", "架构评审"); err != nil {
		t.Fatal(err)
	}
	if svc.gotTitle != "架构评审" {
		t.Errorf("title = %q, want 架构评审", svc.gotTitle)
	}
}

func TestBookNonInteractiveDefaultsWithoutYes(t *testing.T) {
	// -p 给了但没给 -y：非交互不再挂起等标题输入，静默取默认标题
	svc := &fakeBookingSvc{result: bookedResult()}
	a := newAgentTestApp(t, svc, "")
	if _, err := execAppCmd(t, a, newBookCmd, "-d", "07-15", "-t", "14:00-15:00", "-p", "alice bob"); err != nil {
		t.Fatal(err)
	}
	if svc.gotTitle != defaultMeetingTitle {
		t.Errorf("title = %q, want 默认 %q", svc.gotTitle, defaultMeetingTitle)
	}
	if len(svc.gotParticipants) != 2 {
		t.Errorf("participants = %v", svc.gotParticipants)
	}
}

func TestBookUnexpectedStatusIsInternal(t *testing.T) {
	// BookRoom 契约外的状态（如 planned/failed 或未来新增值）不能误报成 no_room
	svc := &fakeBookingSvc{result: &booking.BookResult{
		Status: booking.StatusPlanned, Date: "2026-07-15", StartTime: "14:00:00", EndTime: "15:00:00",
	}}
	a := newAgentTestApp(t, svc, "")
	_, err := execAppCmd(t, a, newBookCmd, "-d", "07-15", "-t", "14:00-15:00")
	if err == nil {
		t.Fatal("契约外状态应报错")
	}
	if e := output.Classify(err); e.Type != output.TypeInternal {
		t.Errorf("error.type = %s, want internal", e.Type)
	}
}

func TestBookInvalidDateIsValidation(t *testing.T) {
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	_, err := execAppCmd(t, a, newBookCmd, "-d", "13-45", "-t", "14:00-15:00")
	if err == nil || output.ExitCode(err) != output.ExitValidation {
		t.Fatalf("非法日期应归 validation: %v", err)
	}
}

// booking.BookStatus 与 output.ErrType 共用一套 agent 词汇表（auto 的
// results[].status 与 book 的 error.type），字面量分居两包，靠此测试钉住同步。
func TestBookStatusErrTypeVocabularyAligned(t *testing.T) {
	pairs := []struct {
		status  booking.BookStatus
		errType output.ErrType
	}{
		{booking.StatusNoRoom, output.TypeNoRoom},
		{booking.StatusConflict, output.TypeConflict},
		{booking.StatusHolidaySkipped, output.TypeHolidaySkipped},
		{booking.StatusNoParticipants, output.TypeNoParticipants},
	}
	for _, p := range pairs {
		if string(p.status) != string(p.errType) {
			t.Errorf("词汇表失同步: booking %q != output %q", p.status, p.errType)
		}
	}
}
