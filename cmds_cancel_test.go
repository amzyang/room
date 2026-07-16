package main

import (
	"encoding/json"
	"errors"
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

func TestCancelBatchNonInteractiveRequiresYes(t *testing.T) {
	svc := &fakeBookingSvc{outcome: &booking.CancelOutcome{}}
	a := newAgentTestApp(t, svc, "")
	_, err := execAppCmd(t, a, newCancelCmd, "--event-id", "evt_1", "--event-id", "evt_2")
	if err == nil {
		t.Fatal("批量无 --yes 非交互应要求确认")
	}
	if got := output.ExitCode(err); got != output.ExitConfirmationRequired {
		t.Errorf("退出码 = %d, want %d", got, output.ExitConfirmationRequired)
	}
	if len(svc.gotCancelIDs) != 0 {
		t.Error("未确认不应执行取消")
	}
}

func TestCancelBatchJSONContinuesOnFailure(t *testing.T) {
	svc := &fakeBookingSvc{
		outcome:    &booking.CancelOutcome{},
		cancelErrs: map[string]error{"evt_2": errors.New("boom")},
	}
	a := newAgentTestApp(t, svc, "")
	a.jsonOut = true

	out, err := execAppCmd(t, a, newCancelCmd,
		"--event-id", "evt_1", "--event-id", "evt_2", "--event-id", "evt_3", "--yes")
	if err != nil {
		t.Fatalf("批量语义整体应 exit 0，单条失败靠 status 区分: %v", err)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Results []struct {
				EventID string `json:"event_id"`
				Status  string `json:"status"`
				Error   string `json:"error"`
			} `json:"results"`
		} `json:"data"`
		Meta struct {
			Count int `json:"count"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("非法 JSON: %v: %s", err, out)
	}
	if !env.OK || env.Meta.Count != 3 || len(env.Data.Results) != 3 {
		t.Fatalf("信封不符: %+v", env)
	}
	wantStatus := []string{"cancelled", "failed", "cancelled"}
	for i, want := range wantStatus {
		if env.Data.Results[i].Status != want {
			t.Errorf("results[%d].status = %s, want %s", i, env.Data.Results[i].Status, want)
		}
	}
	if env.Data.Results[1].Error == "" {
		t.Error("失败条目应含 error 信息")
	}
	if len(svc.gotCancelIDs) != 3 {
		t.Errorf("单条失败不应中断后续: %v", svc.gotCancelIDs)
	}
}

func TestCancelBatchHuman(t *testing.T) {
	svc := &fakeBookingSvc{outcome: &booking.CancelOutcome{AlreadyCancelled: true}}
	a := newAgentTestApp(t, svc, "")

	out, err := execAppCmd(t, a, newCancelCmd, "--event-id", "evt_1", "--event-id", "evt_2", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "evt_1") || !strings.Contains(out, "evt_2") ||
		!strings.Contains(out, "已被取消") {
		t.Errorf("批量人类输出不符: %q", out)
	}
}
