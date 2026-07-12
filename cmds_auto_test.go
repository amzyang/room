package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/amzyang/room/booking"
)

func autoEnvelope(t *testing.T, out string) (bool, []map[string]any, map[string]any) {
	t.Helper()
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Results []map[string]any `json:"results"`
		} `json:"data"`
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("非法 JSON: %v: %s", err, out)
	}
	return env.OK, env.Data.Results, env.Meta
}

func TestAutoDryRunJSON(t *testing.T) {
	svc := &fakeBookingSvc{autoResults: []booking.BookResult{
		{Status: booking.StatusPlanned, Date: "2026-07-17", StartTime: "11:00:00", EndTime: "12:00:00", Title: "周会"},
	}}
	a := newAgentTestApp(t, svc, "")
	a.jsonOut = true
	a.dryRun = true

	out, err := execAppCmd(t, a, newAutoCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !svc.gotAutoDryRun {
		t.Error("--dryrun 应传入 AutoBook")
	}
	ok, results, meta := autoEnvelope(t, out)
	if !ok || len(results) != 1 || results[0]["status"] != "planned" {
		t.Errorf("信封不符: ok=%v results=%v", ok, results)
	}
	if meta["dry_run"] != true || meta["count"] != float64(1) {
		t.Errorf("meta 不符: %v", meta)
	}
}

func TestAutoJSONEmptyResultsIsArray(t *testing.T) {
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	a.jsonOut = true

	out, err := execAppCmd(t, a, newAutoCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"results":[]`) {
		t.Errorf("空结果应输出 [] 而非 null: %s", out)
	}
}

func TestAutoServiceErrorPropagates(t *testing.T) {
	boom := errors.New("task_format broken")
	a := newAgentTestApp(t, &fakeBookingSvc{autoErr: boom}, "")
	if _, err := execAppCmd(t, a, newAutoCmd); !errors.Is(err, boom) {
		t.Errorf("AutoBook 错误应向上传递: %v", err)
	}
}
