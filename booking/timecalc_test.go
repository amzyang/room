package booking

import (
	"strings"
	"testing"
	"time"
)

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", value, shanghai)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestHasTimeOverlap(t *testing.T) {
	at := func(s string) time.Time { return mustTime(t, "2026-07-10 "+s) }

	// 端点相接不算重叠
	if HasTimeOverlap(at("09:00:00"), at("10:00:00"), at("10:00:00"), at("11:00:00")) {
		t.Error("touching endpoints should not overlap")
	}
	if HasTimeOverlap(at("10:00:00"), at("11:00:00"), at("09:00:00"), at("10:00:00")) {
		t.Error("touching endpoints (reversed) should not overlap")
	}
	// 部分重叠
	if !HasTimeOverlap(at("09:00:00"), at("10:30:00"), at("10:00:00"), at("11:00:00")) {
		t.Error("partial overlap should be detected")
	}
	// 包含
	if !HasTimeOverlap(at("09:00:00"), at("12:00:00"), at("10:00:00"), at("11:00:00")) {
		t.Error("containment should be detected")
	}
	if !HasTimeOverlap(at("10:00:00"), at("11:00:00"), at("09:00:00"), at("12:00:00")) {
		t.Error("being contained should be detected")
	}
	// 完全相同
	if !HasTimeOverlap(at("09:00:00"), at("10:00:00"), at("09:00:00"), at("10:00:00")) {
		t.Error("identical ranges should overlap")
	}
	// 不相交
	if HasTimeOverlap(at("09:00:00"), at("10:00:00"), at("11:00:00"), at("12:00:00")) {
		t.Error("disjoint ranges should not overlap")
	}
}

func TestIsOrganizedBy(t *testing.T) {
	myID := "feishu.cn_myprimary@group.calendar.feishu.cn"
	if !IsOrganizedBy(myID, myID) {
		t.Error("same calendar id should be organized by me")
	}
	if IsOrganizedBy("feishu.cn_other@group.calendar.feishu.cn", myID) {
		t.Error("different calendar id should not match")
	}
	if IsOrganizedBy("", myID) {
		t.Error("missing organizer id should not match")
	}
}

func TestFormatEventList(t *testing.T) {
	if got := FormatEventList(nil, 31); got != "未来 31 天内没有日历事件\n" {
		t.Errorf("empty list output = %q", got)
	}

	events := []EventSummary{
		{
			EventID:     "evt-1_0",
			Title:       "团队周会",
			Description: "会议室: 户部巷",
			StartTime:   "2026-07-12 10:00:00",
			EndTime:     "2026-07-12 11:00:00",
			Location:    "户部巷",
		},
		{
			EventID:   "evt-2_0",
			Title:     "无地点会议",
			StartTime: "2026-07-13 14:00:00",
			EndTime:   "2026-07-13 15:00:00",
		},
	}
	got := FormatEventList(events, 31)

	want := "\n未来 31 天的日历事件（共 2 个）:\n" +
		strings.Repeat("=", 80) + "\n" +
		"1. 团队周会\n" +
		"   时间: 2026-07-12 10:00:00 - 2026-07-12 11:00:00\n" +
		"   地点: 户部巷\n" +
		"   描述: 会议室: 户部巷\n" +
		"   事件ID: evt-1_0\n" +
		"\n" +
		"2. 无地点会议\n" +
		"   时间: 2026-07-13 14:00:00 - 2026-07-13 15:00:00\n" +
		"   地点: 无\n" +
		"   事件ID: evt-2_0\n" +
		"\n"
	if got != want {
		t.Errorf("FormatEventList golden mismatch:\ngot:\n%q\nwant:\n%q", got, want)
	}

	for _, line := range strings.Split(got, "\n") {
		if strings.TrimRight(line, " \t") != line {
			t.Errorf("line has trailing whitespace: %q", line)
		}
	}
}

func TestFormatCancelList(t *testing.T) {
	if got := FormatCancelList(nil, 31); got != "未来 31 天内没有你可取消的事件（仅列出你组织的）\n" {
		t.Errorf("empty cancel list output = %q", got)
	}
	got := FormatCancelList([]EventSummary{{EventID: "e", Title: "T", StartTime: "s", EndTime: "e"}}, 7)
	if !strings.HasPrefix(got, "\n未来 7 天可取消的事件（仅你组织的）:\n") {
		t.Errorf("cancel header mismatch: %q", got)
	}
}
