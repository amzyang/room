package main

import (
	"strings"
	"testing"

	"github.com/amzyang/room/booking"
	"github.com/amzyang/room/output"
)

func TestCancelNonInteractiveWithoutEventID(t *testing.T) {
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	_, err := execAppCmd(t, a, newCancelCmd)
	if err == nil {
		t.Fatal("非交互无 --event-id 应 fail-fast 而非挂起")
	}
	if got := output.ExitCode(err); got != output.ExitValidation {
		t.Errorf("退出码 = %d, want %d", got, output.ExitValidation)
	}
	if hint := output.Classify(err).Hint; !strings.Contains(hint, "--event-id") {
		t.Errorf("hint 应指引 --event-id: %q", hint)
	}
}

func TestCancelEventIDWithoutYesNonInteractive(t *testing.T) {
	svc := &fakeBookingSvc{outcome: &booking.CancelOutcome{}}
	a := newAgentTestApp(t, svc, "")
	_, err := execAppCmd(t, a, newCancelCmd, "--event-id", "evt_1")
	if err == nil {
		t.Fatal("无 --yes 非交互应要求确认")
	}
	e := output.Classify(err)
	if e.Type != output.TypeConfirmationRequired {
		t.Errorf("error.type = %s, want confirmation_required", e.Type)
	}
	if got := output.ExitCode(err); got != output.ExitConfirmationRequired {
		t.Errorf("退出码 = %d, want %d", got, output.ExitConfirmationRequired)
	}
	if svc.gotCancelID != "" {
		t.Error("未确认不应执行取消")
	}
}

func TestCancelEventIDWithYesJSON(t *testing.T) {
	svc := &fakeBookingSvc{outcome: &booking.CancelOutcome{}}
	a := newAgentTestApp(t, svc, "")
	a.jsonOut = true

	out, err := execAppCmd(t, a, newCancelCmd, "--event-id", "evt_1", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	got := unwrapData(t, []byte(out))
	if got["event_id"] != "evt_1" || got["status"] != "cancelled" {
		t.Errorf("data 不符: %v", got)
	}
	if svc.gotCancelID != "evt_1" {
		t.Errorf("取消的事件ID = %q", svc.gotCancelID)
	}
}

func TestCancelIdempotentAlreadyCancelled(t *testing.T) {
	svc := &fakeBookingSvc{outcome: &booking.CancelOutcome{AlreadyCancelled: true}}
	a := newAgentTestApp(t, svc, "")
	a.jsonOut = true

	out, err := execAppCmd(t, a, newCancelCmd, "--event-id", "evt_1", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if got := unwrapData(t, []byte(out)); got["status"] != "already_cancelled" {
		t.Errorf("幂等取消 status = %v, want already_cancelled", got["status"])
	}
}

func TestCancelEventIDHuman(t *testing.T) {
	svc := &fakeBookingSvc{outcome: &booking.CancelOutcome{}}
	a := newAgentTestApp(t, svc, "")

	out, err := execAppCmd(t, a, newCancelCmd, "--event-id", "evt_1", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "事件已成功取消") {
		t.Errorf("人类模式输出不符: %q", out)
	}
}
