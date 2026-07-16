package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/amzyang/room/booking"
	"github.com/amzyang/room/output"
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

func TestListDaysWindowFromNow(t *testing.T) {
	svc := &fakeBookingSvc{}
	a := newAgentTestApp(t, svc, "")
	if _, err := execAppCmd(t, a, newListCmd, "-d", "7"); err != nil {
		t.Fatal(err)
	}
	now := a.now().In(a.loc)
	if !svc.gotFrom.Equal(now) || !svc.gotTo.Equal(now.AddDate(0, 0, 7)) {
		t.Errorf("窗口 = [%v, %v), want [now, now+7d)", svc.gotFrom, svc.gotTo)
	}
	if svc.gotMine {
		t.Error("默认不应只看自己组织的")
	}
}

func TestListMineFlagPassesThrough(t *testing.T) {
	svc := &fakeBookingSvc{}
	a := newAgentTestApp(t, svc, "")
	if _, err := execAppCmd(t, a, newListCmd, "--mine"); err != nil {
		t.Fatal(err)
	}
	if !svc.gotMine {
		t.Error("--mine 应传递 organizedByMeOnly=true")
	}
}

func TestListDateSingleDayWindow(t *testing.T) {
	svc := &fakeBookingSvc{}
	a := newAgentTestApp(t, svc, "")
	a.jsonOut = true

	out, err := execAppCmd(t, a, newListCmd, "--date", "07-20")
	if err != nil {
		t.Fatal(err)
	}
	// 测试 app 的 now 为 2026-07-10：MM-DD 应补当年
	wantFrom := mustDate(t, a, "2026-07-20")
	if !svc.gotFrom.Equal(wantFrom) || !svc.gotTo.Equal(wantFrom.AddDate(0, 0, 1)) {
		t.Errorf("窗口 = [%v, %v), want [%v, +1d)", svc.gotFrom, svc.gotTo, wantFrom)
	}
	if !strings.Contains(out, `"date":"2026-07-20"`) {
		t.Errorf("--date 模式 data 应含 date 字段: %s", out)
	}
	if strings.Contains(out, `"days"`) {
		t.Errorf("--date 模式 data 不应含 days 字段: %s", out)
	}
}

func TestListDateDaysMutuallyExclusive(t *testing.T) {
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	_, err := execAppCmd(t, a, newListCmd, "--date", "07-20", "-d", "7")
	if err == nil {
		t.Fatal("--date 与 --days 同给应报错")
	}
	if got := output.ExitCode(err); got != output.ExitValidation {
		t.Errorf("退出码 = %d, want %d", got, output.ExitValidation)
	}
}

func TestListInvalidDaysRejected(t *testing.T) {
	for _, d := range []string{"0", "-3"} {
		a := newAgentTestApp(t, &fakeBookingSvc{}, "")
		_, err := execAppCmd(t, a, newListCmd, "-d", d)
		if err == nil {
			t.Fatalf("-d %s 应报错", d)
		}
		if got := output.ExitCode(err); got != output.ExitValidation {
			t.Errorf("-d %s 退出码 = %d, want %d", d, got, output.ExitValidation)
		}
	}
}

func TestListInvalidDateRejected(t *testing.T) {
	a := newAgentTestApp(t, &fakeBookingSvc{}, "")
	_, err := execAppCmd(t, a, newListCmd, "--date", "13-45")
	if err == nil {
		t.Fatal("非法日期应报错")
	}
	if got := output.ExitCode(err); got != output.ExitValidation {
		t.Errorf("退出码 = %d, want %d", got, output.ExitValidation)
	}
}

// mustDate 在测试 app 的时区解析 YYYY-MM-DD 零点。
func mustDate(t *testing.T, a *app, s string) time.Time {
	t.Helper()
	d, err := time.ParseInLocation("2006-01-02", s, a.loc)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
