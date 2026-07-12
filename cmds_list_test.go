package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/amzyang/room/booking"
)

func TestListJSON(t *testing.T) {
	svc := &fakeBookingSvc{events: []booking.EventSummary{
		{EventID: "evt_1", Title: "周会", Location: "3F-A",
			StartTime: "2026-07-15 14:00:00", EndTime: "2026-07-15 15:00:00",
			StartTimestamp: 1789452000, EndTimestamp: 1789455600},
	}}
	a := newAgentTestApp(t, svc, "")
	a.jsonOut = true

	out, err := execAppCmd(t, a, newListCmd, "-d", "7")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Days   int                    `json:"days"`
			Events []booking.EventSummary `json:"events"`
		} `json:"data"`
		Meta struct {
			Count int `json:"count"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("非法 JSON: %v: %s", err, out)
	}
	if !env.OK || env.Data.Days != 7 || env.Meta.Count != 1 {
		t.Errorf("信封不符: %+v", env)
	}
	if env.Data.Events[0].EventID != "evt_1" || env.Data.Events[0].Title != "周会" {
		t.Errorf("事件字段不符: %+v", env.Data.Events[0])
	}
}

func TestListJSONEmptyIsArray(t *testing.T) {
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	a.jsonOut = true

	out, err := execAppCmd(t, a, newListCmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"events":[]`) {
		t.Errorf("空列表应输出 [] 而非 null: %s", out)
	}
}

func TestListHumanUnchanged(t *testing.T) {
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	out, err := execAppCmd(t, a, newListCmd, "-d", "7")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "没有日历事件") {
		t.Errorf("人类模式输出不符: %q", out)
	}
}
