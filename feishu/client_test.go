package feishu

import (
	"reflect"
	"testing"
	"time"
)

func TestSplitWindows(t *testing.T) {
	start := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	t.Run("跨度不超过上限时保持单窗口", func(t *testing.T) {
		got := splitWindows(start, start.Add(31*day), 39*day)
		want := []timeWindow{{start, start.Add(31 * day)}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("超出上限时切成首尾相接的窗口", func(t *testing.T) {
		got := splitWindows(start, start.Add(90*day), 39*day)
		want := []timeWindow{
			{start, start.Add(39 * day)},
			{start.Add(39 * day), start.Add(78 * day)},
			{start.Add(78 * day), start.Add(90 * day)},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func TestMergeCalendarEvents(t *testing.T) {
	instances := []CalendarEvent{
		{EventID: "weekly_1790044200", Summary: "AIGC周例会", StartTimestamp: 1790044200, Status: "confirmed"},
		{EventID: "leave_0", Summary: "休假", StartTimestamp: 1790006459, Status: "confirmed"},
		// 跨窗口边界的事件会被相邻两个窗口各返回一次
		{EventID: "leave_0", Summary: "休假", StartTimestamp: 1790006459, Status: "confirmed"},
	}
	listed := []CalendarEvent{
		// 重复日程的母事件：时间戳是首次发生时间，由实例视图取代
		{EventID: "weekly_0", Summary: "AIGC周例会", StartTimestamp: 1772505000, Status: "confirmed"},
		{EventID: "leave_0", Summary: "休假", StartTimestamp: 1790006459, Status: "confirmed"},
		{EventID: "auto_0", Summary: "meeting", StartTimestamp: 1789970400, Status: "cancelled"},
		{EventID: "gone_0", Summary: "meeting", StartTimestamp: 1789974000, Status: "deleted"},
	}

	got := mergeCalendarEvents(instances, listed)
	want := []CalendarEvent{
		{EventID: "weekly_1790044200", Summary: "AIGC周例会", StartTimestamp: 1790044200, Status: "confirmed"},
		{EventID: "leave_0", Summary: "休假", StartTimestamp: 1790006459, Status: "confirmed"},
		{EventID: "auto_0", Summary: "meeting", StartTimestamp: 1789970400, Status: "cancelled"},
		{EventID: "gone_0", Summary: "meeting", StartTimestamp: 1789974000, Status: "deleted"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}
