package booking

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFormatTaskRoundTrip(t *testing.T) {
	cases := []string{
		"wed,10:00:00-11:30:00,weekly:2:2026-07-01,alice:bob,周会",
		"fri,14:00:00-15:00:00,weekly,,复盘",
		"mon,09:30:00-09:45:00,daily,,站会",
		"thu,16:00:00-17:00:00,monthly:1:2026-07-02,carol,月度复盘",
		"tue,11:00:00-12:00:00,,,无频率任务",
	}
	for _, dsl := range cases {
		tasks := ParseTaskFormat(dsl)
		if len(tasks) != 1 {
			t.Fatalf("ParseTaskFormat(%q) got %d tasks", dsl, len(tasks))
		}
		got := FormatTask(tasks[0])
		if !reflect.DeepEqual(ParseTaskFormat(got), tasks) {
			t.Errorf("round trip broken: %q → %q", dsl, got)
		}
	}
}

func TestFormatTaskOmitsDefaultInterval(t *testing.T) {
	task := ParseTaskFormat("fri,14:00:00-15:00:00,weekly,,复盘")[0]
	if got := FormatTask(task); got != "fri,14:00:00-15:00:00,weekly,,复盘" {
		t.Errorf("interval=1 无锚点应省略后缀, got %q", got)
	}
	task = ParseTaskFormat("fri,14:00:00-15:00:00,weekly:2,,复盘")[0]
	if got := FormatTask(task); got != "fri,14:00:00-15:00:00,weekly:2,,复盘" {
		t.Errorf("interval=2 无锚点应保留间隔, got %q", got)
	}
}

func TestFormatTasksJoinsWithPipe(t *testing.T) {
	dsl := "wed,10:00:00-11:30:00,weekly,,周会|fri,14:00:00-15:00:00,weekly,,复盘"
	tasks := ParseTaskFormat(dsl)
	if got := FormatTasks(tasks); got != dsl {
		t.Errorf("FormatTasks = %q, want %q", got, dsl)
	}
	if FormatTasks(nil) != "" {
		t.Errorf("空任务列表应序列化为空串")
	}
}

func TestDescribeTask(t *testing.T) {
	cases := []struct {
		dsl  string
		want string
	}{
		{"wed,10:00:00-11:30:00,weekly:2:2026-07-01,alice:bob,周会",
			"周三 10:00–11:30 · 每2周（自2026-07-01） · 周会 · alice bob"},
		{"fri,14:00:00-15:00:00,weekly,,复盘", "周五 14:00–15:00 · 每周 · 复盘"},
		{"mon,09:30:00-09:45:00,daily,,站会", "周一 09:30–09:45 · 每天 · 站会"},
		{"thu,16:00:00-17:00:00,monthly,,月度会", "周四 16:00–17:00 · 每月 · 月度会"},
		{"tue,11:00:00-12:00:00,,,无频率", "周二 11:00–12:00 · 每周 · 无频率"},
		{"sat,08:00:00-09:00:00,weekly,,", "周六 08:00–09:00 · 每周 · （无标题）"},
	}
	for _, c := range cases {
		task := ParseTaskFormat(c.dsl)[0]
		if got := DescribeTask(task); got != c.want {
			t.Errorf("DescribeTask(%q)\n got  %q\n want %q", c.dsl, got, c.want)
		}
	}
}

func TestNextOccurrences(t *testing.T) {
	// 2026-07-12 是周日
	from := time.Date(2026, 7, 12, 9, 0, 0, 0, shanghai)

	task := ParseTaskFormat("wed,10:00:00-11:00:00,weekly,,周会")[0]
	got := NextOccurrences(task, from, 3, shanghai)
	want := []string{"2026-07-15", "2026-07-22", "2026-07-29"}
	assertDates(t, got, want)

	// 隔周 + 锚点
	task = ParseTaskFormat("wed,10:00:00-11:00:00,weekly:2:2026-07-15,,周会")[0]
	got = NextOccurrences(task, from, 3, shanghai)
	assertDates(t, got, []string{"2026-07-15", "2026-07-29", "2026-08-12"})

	// 锚点在未来：之前的日期不命中
	task = ParseTaskFormat("wed,10:00:00-11:00:00,weekly:1:2026-08-05,,周会")[0]
	got = NextOccurrences(task, from, 2, shanghai)
	assertDates(t, got, []string{"2026-08-05", "2026-08-12"})

	// 起始日当天命中（对齐 processTask 从 now 起含当日遍历）
	task = ParseTaskFormat("sun,10:00:00-11:00:00,weekly,,晨会")[0]
	got = NextOccurrences(task, from, 1, shanghai)
	assertDates(t, got, []string{"2026-07-12"})

	// 非法星期几：无命中
	task = ParseTaskFormat("someday,10:00:00-11:00:00,weekly,,X")[0]
	if got := NextOccurrences(task, from, 3, shanghai); len(got) != 0 {
		t.Errorf("非法星期几应无命中, got %v", got)
	}
}

func assertDates(t *testing.T, got []time.Time, want []string) {
	t.Helper()
	var gotStr []string
	for _, d := range got {
		gotStr = append(gotStr, d.Format("2006-01-02"))
	}
	if !reflect.DeepEqual(gotStr, want) {
		t.Errorf("occurrences = %v, want %v", gotStr, want)
	}
}

// 历史配置的无锚点间隔运行时被 IsInCycle 忽略（恒按每周期命中），归一为 1 后
// DescribeTask 如实显示「每周」，且序列化必过严格校验——编辑器保存不被
// 用户未改动的旧任务卡死。
func TestCanonicalizeTasks(t *testing.T) {
	legacy := "wed,10:00:00-11:00:00,weekly:2,,周会|fri,14:00:00-15:00:00,weekly:2:2026-07-17,,复盘"
	tasks := CanonicalizeTasks(ParseTaskFormat(legacy))
	if tasks[0].Interval != 1 {
		t.Errorf("无锚点间隔应归一为 1: %+v", tasks[0])
	}
	if got := DescribeTask(tasks[0]); !strings.Contains(got, "每周") {
		t.Errorf("归一后应如实描述为每周: %q", got)
	}
	if tasks[1].Interval != 2 || tasks[1].StartDate != "2026-07-17" {
		t.Errorf("带锚点的间隔应原样保留: %+v", tasks[1])
	}
	if err := ValidateTaskFormat(FormatTasks(tasks)); err != nil {
		t.Errorf("归一后的序列化必须通过严格校验: %v", err)
	}
}

func TestValidateTaskFormatAccepts(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"wed,10:00:00-11:30:00,weekly:2:2026-07-01,alice:bob,周会",
		"fri,14:00:00-15:00:00,weekly,,复盘|mon,09:30:00-09:45:00,daily,,站会",
		"tue,11:00:00-12:00:00,,,无频率无参与者",
		"sat,08:00:00-09:00:00,weekly,,",    // 空标题运行时可预订，放行
		"WED,10:00:00-11:00:00,WEEKLY,,大小写", // 运行时大小写不敏感
	}
	for _, dsl := range cases {
		if err := ValidateTaskFormat(dsl); err != nil {
			t.Errorf("ValidateTaskFormat(%q) = %v, want nil", dsl, err)
		}
	}
}

func TestValidateTaskFormatRejects(t *testing.T) {
	cases := []struct {
		dsl     string
		keyword string // 错误信息必须包含的片段
	}{
		{"wedn,10:00:00-11:00:00,weekly,,X", "星期几"},
		{",10:00:00-11:00:00,weekly,,X", "星期几"},
		{"wed,,weekly,,X", "时间段"},
		{"wed,10:00-11:00,weekly,,X", "10:00:00"}, // HH:MM 运行时解析失败，提示正确格式
		{"wed,11:00:00-10:00:00,weekly,,X", "早于"},
		{"wed,10:00:00~11:00:00,weekly,,X", "时间段"},
		{"wed,10:00:00-11:00:00,yearly,,X", "频率"},
		{"wed,10:00:00-11:00:00,weekly:x,,X", "间隔"},
		{"wed,10:00:00-11:00:00,weekly:0,,X", "间隔"},
		{"wed,10:00:00-11:00:00,weekly:2,,X", "锚点"}, // 间隔>1 无锚点 = 间隔不生效
		{"wed,10:00:00-11:00:00,weekly:2:07-01,,X", "锚点"},
		{"wed,10:00:00-11:00:00,weekly:2:2026-07-01:extra,,X", "频率"},
		{"wed,10:00:00-11:00:00,weekly,,标题,含逗号", "逗号"},
		{"wed,10:00:00-11:00:00,weekly,,OK|fried,10:00:00-11:00:00,weekly,,X", "第 2 条"},
	}
	for _, c := range cases {
		err := ValidateTaskFormat(c.dsl)
		if err == nil {
			t.Errorf("ValidateTaskFormat(%q) = nil, want error", c.dsl)
			continue
		}
		if !strings.Contains(err.Error(), c.keyword) {
			t.Errorf("ValidateTaskFormat(%q) = %q, want contains %q", c.dsl, err, c.keyword)
		}
	}
}
