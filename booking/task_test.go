package booking

import (
	"reflect"
	"testing"
	"time"
)

var shanghai, _ = time.LoadLocation("Asia/Shanghai")

func TestParseParticipants(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  ", nil},
		{"a:b:c", []string{"a", "b", "c"}},
		{"a b c", []string{"a", "b", "c"}},
		{"a:b c", []string{"a", "b", "c"}},
		{"a　b", []string{"a", "b"}}, // 全角空格
		{"oc_abc123:zhangsan", []string{"oc_abc123", "zhangsan"}},
	}
	for _, c := range cases {
		if got := ParseParticipants(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseParticipants(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseTaskFormat(t *testing.T) {
	tasks := ParseTaskFormat("fri,11:00:00-12:00:00,weekly,alice:bob,项目周会")
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(tasks))
	}
	task := tasks[0]
	if task.DayOfWeek != "fri" || task.StartTime != "11:00:00" || task.EndTime != "12:00:00" ||
		task.Frequency != "weekly" || task.Interval != 1 || task.StartDate != "" || task.Title != "项目周会" {
		t.Errorf("unexpected task: %+v", task)
	}
	if !reflect.DeepEqual(task.Participants, []string{"alice", "bob"}) {
		t.Errorf("participants = %v", task.Participants)
	}

	// interval + startDate
	tasks = ParseTaskFormat("mon,17:30:00-18:30:00,weekly:2:2025-04-21,alice,AI例会")
	task = tasks[0]
	if task.Frequency != "weekly" || task.Interval != 2 || task.StartDate != "2025-04-21" {
		t.Errorf("unexpected freq config: %+v", task)
	}
	if !reflect.DeepEqual(task.Participants, []string{"alice"}) {
		t.Errorf("participants = %v", task.Participants)
	}

	// 非法 interval 回退 1；非法 startDate 置空
	task = ParseTaskFormat("mon,09:00:00-10:00:00,weekly:0:not-a-date,,X")[0]
	if task.Interval != 1 || task.StartDate != "" {
		t.Errorf("invalid freq parts should fall back: %+v", task)
	}

	// 多任务
	tasks = ParseTaskFormat("fri,09:00:00-09:30:00,weekly,,A|mon,10:00:00-11:00:00,daily,,B")
	if len(tasks) != 2 || tasks[0].Title != "A" || tasks[1].Title != "B" {
		t.Errorf("multi-task parse failed: %+v", tasks)
	}

	// 空任务段跳过
	if got := ParseTaskFormat(" | "); got != nil {
		t.Errorf("blank tasks should be skipped, got %+v", got)
	}
}

func TestIsDayOfWeekMatch(t *testing.T) {
	friday := time.Date(2026, 7, 10, 12, 0, 0, 0, shanghai)
	if !IsDayOfWeekMatch(friday, "fri") || !IsDayOfWeekMatch(friday, "FRI") {
		t.Error("friday should match fri")
	}
	if IsDayOfWeekMatch(friday, "mon") || IsDayOfWeekMatch(friday, "xxx") {
		t.Error("friday should not match mon/xxx")
	}
}

func TestIsInCycle(t *testing.T) {
	weekly2 := Task{Frequency: "weekly", Interval: 2, StartDate: "2025-04-21"} // 周一锚点

	// 锚点当周 → 命中
	if !IsInCycle(time.Date(2025, 4, 21, 9, 0, 0, 0, shanghai), weekly2, shanghai) {
		t.Error("anchor day should be in cycle")
	}
	// 锚点次周 → 不命中
	if IsInCycle(time.Date(2025, 4, 28, 9, 0, 0, 0, shanghai), weekly2, shanghai) {
		t.Error("anchor+7d should be out of cycle for weekly:2")
	}
	// 锚点+14 天 → 命中
	if !IsInCycle(time.Date(2025, 5, 5, 9, 0, 0, 0, shanghai), weekly2, shanghai) {
		t.Error("anchor+14d should be in cycle")
	}
	// 锚点前一周 → 排除（floor 除法产生负 weeksDiff）
	if IsInCycle(time.Date(2025, 4, 14, 9, 0, 0, 0, shanghai), weekly2, shanghai) {
		t.Error("dates a week before anchor should be out of cycle")
	}

	// 无锚点 → 恒 true
	if !IsInCycle(time.Date(2025, 4, 14, 9, 0, 0, 0, shanghai), Task{Frequency: "weekly", Interval: 2}, shanghai) {
		t.Error("no anchor should always be in cycle")
	}

	daily3 := Task{Frequency: "daily", Interval: 3, StartDate: "2025-04-21"}
	if !IsInCycle(time.Date(2025, 4, 24, 9, 0, 0, 0, shanghai), daily3, shanghai) {
		t.Error("anchor+3d should be in daily:3 cycle")
	}
	if IsInCycle(time.Date(2025, 4, 23, 9, 0, 0, 0, shanghai), daily3, shanghai) {
		t.Error("anchor+2d should be out of daily:3 cycle")
	}

	monthly2 := Task{Frequency: "monthly", Interval: 2, StartDate: "2025-01-15"}
	if !IsInCycle(time.Date(2025, 3, 15, 9, 0, 0, 0, shanghai), monthly2, shanghai) {
		t.Error("anchor+2mo should be in monthly:2 cycle")
	}
	if IsInCycle(time.Date(2025, 2, 15, 9, 0, 0, 0, shanghai), monthly2, shanghai) {
		t.Error("anchor+1mo should be out of monthly:2 cycle")
	}
}

func TestFloorDiv(t *testing.T) {
	cases := []struct{ a, b, want int }{
		{7, 7, 1}, {6, 7, 0}, {0, 7, 0}, {-1, 7, -1}, {-7, 7, -1}, {-8, 7, -2}, {14, 7, 2},
	}
	for _, c := range cases {
		if got := floorDiv(c.a, c.b); got != c.want {
			t.Errorf("floorDiv(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
