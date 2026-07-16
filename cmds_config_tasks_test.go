package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/amzyang/room/booking"
	"github.com/amzyang/room/output"
)

var taskTestLoc, _ = time.LoadLocation("Asia/Shanghai")

// 2026-07-12 是周日
var taskTestNow = time.Date(2026, 7, 12, 9, 0, 0, 0, taskTestLoc)

// fakeTaskUI 脚本化的编辑器交互：菜单按 label 片段匹配（"<esc>" 表示取消），
// 表单按注入函数改写输入，confirm 按序应答并记录预览文本。
type fakeTaskUI struct {
	t             *testing.T
	menuScript    []string
	formScript    []func(in taskFormInput, multiDay bool) (taskFormInput, bool)
	confirmScript []bool
	menuTitles    []string
	previews      []string
}

func (f *fakeTaskUI) menu(title, _ string, options []string) (int, bool, error) {
	f.t.Helper()
	f.menuTitles = append(f.menuTitles, title)
	if len(f.menuScript) == 0 {
		f.t.Fatal("菜单脚本已耗尽，仍在弹出菜单")
	}
	want := f.menuScript[0]
	f.menuScript = f.menuScript[1:]
	if want == "<esc>" {
		return 0, false, nil
	}
	for i, o := range options {
		if strings.Contains(o, want) {
			return i, true, nil
		}
	}
	f.t.Fatalf("菜单中找不到 %q，实际选项: %v", want, options)
	return 0, false, nil
}

func (f *fakeTaskUI) taskForm(in taskFormInput, multiDay bool) (taskFormInput, bool, error) {
	f.t.Helper()
	if len(f.formScript) == 0 {
		f.t.Fatal("表单脚本已耗尽，仍在弹出表单")
	}
	fn := f.formScript[0]
	f.formScript = f.formScript[1:]
	out, ok := fn(in, multiDay)
	return out, ok, nil
}

func (f *fakeTaskUI) confirm(preview string) (bool, error) {
	f.t.Helper()
	f.previews = append(f.previews, preview)
	if len(f.confirmScript) == 0 {
		f.t.Fatal("confirm 脚本已耗尽")
	}
	ans := f.confirmScript[0]
	f.confirmScript = f.confirmScript[1:]
	return ans, nil
}

func twoTasks() []booking.Task {
	return booking.ParseTaskFormat("wed,10:00:00-11:30:00,weekly:2:2026-07-01,alice:bob,周会|fri,14:00:00-15:00:00,weekly,,复盘")
}

func TestBuildTaskMenu(t *testing.T) {
	opts := buildTaskMenu(twoTasks())
	if len(opts) != 5 {
		t.Fatalf("2 个任务应有 5 项（任务×2+新增+保存+放弃），实际 %d: %v", len(opts), opts)
	}
	if opts[0].Action != taskMenuTask || opts[0].Index != 0 || !strings.Contains(opts[0].Label, "周会") {
		t.Errorf("任务项应为人话摘要: %+v", opts[0])
	}
	if opts[2].Action != taskMenuAdd || opts[3].Action != taskMenuSave || opts[4].Action != taskMenuQuit {
		t.Errorf("尾部应为 新增/保存/放弃: %+v", opts[2:])
	}

	// 空状态仍保留保存（删光已有任务后需要保存来清空配置）
	empty := buildTaskMenu(nil)
	if len(empty) != 3 {
		t.Fatalf("空状态应为 新增+保存+放弃，实际 %v", empty)
	}
}

func TestManageTasksAddMultiDaySave(t *testing.T) {
	ui := &fakeTaskUI{t: t,
		menuScript: []string{"新增", "保存"},
		formScript: []func(taskFormInput, bool) (taskFormInput, bool){
			func(in taskFormInput, multiDay bool) (taskFormInput, bool) {
				if !multiDay {
					t.Error("新增应为星期多选")
				}
				if in.Frequency != "weekly" || in.Interval != "1" {
					t.Errorf("新增表单应预置 weekly/1: %+v", in)
				}
				in.Days = []string{"mon", "wed", "fri"}
				in.TimeRange = "09:30-09:45"
				in.Title = "站会"
				return in, true
			},
		},
		confirmScript: []bool{true},
	}
	tasks, saved, err := manageTasks(nil, ui, taskTestNow, taskTestLoc)
	if err != nil || !saved {
		t.Fatalf("saved=%v err=%v", saved, err)
	}
	want := "mon,09:30:00-09:45:00,weekly,,站会|wed,09:30:00-09:45:00,weekly,,站会|fri,09:30:00-09:45:00,weekly,,站会"
	if got := booking.FormatTasks(tasks); got != want {
		t.Errorf("多选展开:\n got  %s\n want %s", got, want)
	}
	if len(ui.previews) != 1 || !strings.Contains(ui.previews[0], "3 条任务") {
		t.Errorf("预览应汇总 3 条任务: %v", ui.previews)
	}
	if !strings.Contains(ui.menuTitles[0], "未配置") {
		t.Errorf("空状态菜单标题应提示未配置: %q", ui.menuTitles[0])
	}
}

func TestManageTasksEditKeepsOrder(t *testing.T) {
	ui := &fakeTaskUI{t: t,
		menuScript: []string{"周会", "编辑", "保存"},
		formScript: []func(taskFormInput, bool) (taskFormInput, bool){
			func(in taskFormInput, multiDay bool) (taskFormInput, bool) {
				if multiDay {
					t.Error("编辑应为星期单选")
				}
				if in.Title != "周会" || in.TimeRange != "10:00:00-11:30:00" || in.StartDate != "2026-07-01" {
					t.Errorf("编辑表单应预填现值: %+v", in)
				}
				in.Title = "架构周会"
				return in, true
			},
		},
		confirmScript: []bool{true},
	}
	tasks, saved, err := manageTasks(twoTasks(), ui, taskTestNow, taskTestLoc)
	if err != nil || !saved {
		t.Fatalf("saved=%v err=%v", saved, err)
	}
	if len(tasks) != 2 || tasks[0].Title != "架构周会" || tasks[1].Title != "复盘" {
		t.Errorf("应原位更新第 1 条: %v", booking.FormatTasks(tasks))
	}
	if len(tasks[0].Participants) != 2 || tasks[0].StartDate != "2026-07-01" {
		t.Errorf("未改字段应保留: %+v", tasks[0])
	}
}

func TestManageTasksCopyAndDelete(t *testing.T) {
	ui := &fakeTaskUI{t: t,
		menuScript: []string{"周会", "复制", "复盘", "删除", "保存"},
		formScript: []func(taskFormInput, bool) (taskFormInput, bool){
			func(in taskFormInput, multiDay bool) (taskFormInput, bool) {
				if !multiDay {
					t.Error("复制应支持星期多选")
				}
				if in.Title != "周会" {
					t.Errorf("复制表单应预填源任务: %+v", in)
				}
				in.Days = []string{"thu"}
				return in, true
			},
		},
		confirmScript: []bool{true},
	}
	tasks, saved, err := manageTasks(twoTasks(), ui, taskTestNow, taskTestLoc)
	if err != nil || !saved {
		t.Fatalf("saved=%v err=%v", saved, err)
	}
	got := booking.FormatTasks(tasks)
	want := "wed,10:00:00-11:30:00,weekly:2:2026-07-01,alice:bob,周会|thu,10:00:00-11:30:00,weekly:2:2026-07-01,alice:bob,周会"
	if got != want {
		t.Errorf("复制追加+删除复盘:\n got  %s\n want %s", got, want)
	}
}

func TestManageTasksQuitAndEsc(t *testing.T) {
	for _, script := range [][]string{{"放弃"}, {"<esc>"}} {
		ui := &fakeTaskUI{t: t, menuScript: script}
		_, saved, err := manageTasks(twoTasks(), ui, taskTestNow, taskTestLoc)
		if err != nil || saved {
			t.Errorf("script %v: 应放弃不保存, saved=%v err=%v", script, saved, err)
		}
	}
}

func TestManageTasksCancelledStepsKeepTasks(t *testing.T) {
	ui := &fakeTaskUI{t: t,
		// 新增被表单取消；再新增但预览拒绝；子菜单返回；最后保存
		menuScript: []string{"新增", "新增", "周会", "返回", "保存"},
		formScript: []func(taskFormInput, bool) (taskFormInput, bool){
			func(in taskFormInput, _ bool) (taskFormInput, bool) { return in, false },
			func(in taskFormInput, _ bool) (taskFormInput, bool) {
				in.Days = []string{"sat"}
				in.TimeRange = "08:00-09:00"
				in.Title = "临时"
				return in, true
			},
		},
		confirmScript: []bool{false},
	}
	tasks, saved, err := manageTasks(twoTasks(), ui, taskTestNow, taskTestLoc)
	if err != nil || !saved {
		t.Fatalf("saved=%v err=%v", saved, err)
	}
	if len(tasks) != 2 {
		t.Errorf("取消的步骤不应改动任务: %v", booking.FormatTasks(tasks))
	}
}

func TestNormalizeTaskInput(t *testing.T) {
	// HH:MM 补秒；间隔>1 未填锚点 → 自动取该星期几的下一个命中日
	tasks := normalizeTaskInput(taskFormInput{
		Days: []string{"wed", "fri"}, TimeRange: "10:00-11:30",
		Frequency: "weekly", Interval: "2", Participants: "alice bob", Title: "周会",
	}, taskTestNow, taskTestLoc)
	if len(tasks) != 2 {
		t.Fatalf("应展开 2 条: %v", tasks)
	}
	if tasks[0].StartTime != "10:00:00" || tasks[0].EndTime != "11:30:00" {
		t.Errorf("HH:MM 应补秒: %+v", tasks[0])
	}
	if tasks[0].StartDate != "2026-07-15" || tasks[1].StartDate != "2026-07-17" {
		t.Errorf("自动锚点应为各自星期几的下一个命中日: %s / %s", tasks[0].StartDate, tasks[1].StartDate)
	}
	if tasks[0].Interval != 2 || len(tasks[0].Participants) != 2 {
		t.Errorf("间隔/参与者解析: %+v", tasks[0])
	}

	// 间隔 1：不自动补锚点；显式锚点保留；HH:MM:SS 原样
	tasks = normalizeTaskInput(taskFormInput{
		Days: []string{"mon"}, TimeRange: "09:00:00-10:00:00",
		Frequency: "weekly", Interval: "1", StartDate: "2026-08-03", Title: "X",
	}, taskTestNow, taskTestLoc)
	if tasks[0].StartDate != "2026-08-03" || tasks[0].StartTime != "09:00:00" {
		t.Errorf("显式锚点/带秒时间应原样保留: %+v", tasks[0])
	}

	// 间隔留空 = 1
	tasks = normalizeTaskInput(taskFormInput{
		Days: []string{"mon"}, TimeRange: "09:00-10:00", Frequency: "weekly", Title: "X",
	}, taskTestNow, taskTestLoc)
	if tasks[0].Interval != 1 || tasks[0].StartDate != "" {
		t.Errorf("空间隔应为 1 且不补锚点: %+v", tasks[0])
	}
}

func TestTaskFormInputRoundTrip(t *testing.T) {
	for _, task := range twoTasks() {
		back := normalizeTaskInput(taskToFormInput(task), taskTestNow, taskTestLoc)
		if got, want := booking.FormatTasks(back), booking.FormatTask(task); got != want {
			t.Errorf("编辑往返不保真:\n got  %s\n want %s", got, want)
		}
	}
}

func TestTaskFieldValidators(t *testing.T) {
	if err := validateTaskDays(nil); err == nil {
		t.Error("星期几至少选一个")
	}
	if err := validateTaskDays([]string{"mon"}); err != nil {
		t.Error(err)
	}
	timeCases := map[string]bool{
		"10:00-11:30": true, "10:00:00-11:30:00": true, "10:00-11:30:00": true,
		"": false, "10:00": false, "11:30-10:00": false, "10:00-10:00": false, "25:00-26:00": false,
	}
	for in, ok := range timeCases {
		if err := validateTaskTimeRange(in); (err == nil) != ok {
			t.Errorf("validateTaskTimeRange(%q) = %v, want ok=%v", in, err, ok)
		}
	}
	intervalCases := map[string]bool{"": true, "1": true, "4": true, "0": false, "x": false, "-1": false}
	for in, ok := range intervalCases {
		if err := validateTaskInterval(in); (err == nil) != ok {
			t.Errorf("validateTaskInterval(%q) = %v, want ok=%v", in, err, ok)
		}
	}
	dateCases := map[string]bool{"": true, "2026-07-01": true, "07-01": false, "2026/07/01": false}
	for in, ok := range dateCases {
		if err := validateTaskDate(in); (err == nil) != ok {
			t.Errorf("validateTaskDate(%q) = %v, want ok=%v", in, err, ok)
		}
	}
	titleCases := map[string]bool{"周会": true, "": false, "a,b": false, "a|b": false}
	for in, ok := range titleCases {
		if err := validateTaskTitle(in); (err == nil) != ok {
			t.Errorf("validateTaskTitle(%q) = %v, want ok=%v", in, err, ok)
		}
	}
	partCases := map[string]bool{"": true, "alice bob": true, "alice:bob": true, "a,b": false, "a|b": false}
	for in, ok := range partCases {
		if err := validateTaskParticipants(in); (err == nil) != ok {
			t.Errorf("validateTaskParticipants(%q) = %v, want ok=%v", in, err, ok)
		}
	}
}

func TestPreviewTasks(t *testing.T) {
	tasks := booking.ParseTaskFormat("wed,10:00:00-11:30:00,weekly:2:2026-07-15,alice:bob,周会")
	got := previewTasks(tasks, taskTestNow, taskTestLoc)
	for _, want := range []string{
		"1 条任务",
		"周三 10:00–11:30 · 每2周（自2026-07-15） · 周会 · alice bob",
		"DSL: wed,10:00:00-11:30:00,weekly:2:2026-07-15,alice:bob,周会",
		"2026-07-15、2026-07-29、2026-08-12",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("预览缺少 %q:\n%s", want, got)
		}
	}

	// 永不命中的任务给出提示而不是空白
	never := booking.Task{DayOfWeek: "wed", StartTime: "10:00:00", EndTime: "11:00:00",
		Frequency: "weekly", Interval: 1, StartDate: "2099-01-01"}
	if got := previewTasks([]booking.Task{never}, taskTestNow, taskTestLoc); !strings.Contains(got, "命中：无") {
		t.Errorf("扫描窗口内无命中应提示: %s", got)
	}
}

func TestConfigTasksJSON(t *testing.T) {
	t.Setenv("TASK_FORMAT",
		"fri,11:00:00-12:00:00,weekly,alice:bob,项目周会|mon,09:00:00-09:30:00,daily:2:2026-07-01,,站会")
	a := newConfigTestApp("", nil)
	a.jsonOut = true

	out, _, err := execConfigCmd(t, a, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Raw   string `json:"raw"`
			Tasks []struct {
				DayOfWeek    string   `json:"day_of_week"`
				StartTime    string   `json:"start_time"`
				Frequency    string   `json:"frequency"`
				Interval     int      `json:"interval"`
				StartDate    string   `json:"start_date"`
				Participants []string `json:"participants"`
				Title        string   `json:"title"`
			} `json:"tasks"`
		} `json:"data"`
		Meta struct {
			Count int `json:"count"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("非法 JSON: %v: %s", err, out)
	}
	if !env.OK || env.Meta.Count != 2 || len(env.Data.Tasks) != 2 || env.Data.Raw == "" {
		t.Fatalf("信封不符: %+v", env)
	}
	first, second := env.Data.Tasks[0], env.Data.Tasks[1]
	if first.DayOfWeek != "fri" || first.Frequency != "weekly" || first.Interval != 1 ||
		first.Title != "项目周会" || len(first.Participants) != 2 {
		t.Errorf("第一条任务解析不符: %+v", first)
	}
	if second.Interval != 2 || second.StartDate != "2026-07-01" || len(second.Participants) != 0 {
		t.Errorf("第二条任务解析不符: %+v", second)
	}
}

func TestConfigTasksJSONEmpty(t *testing.T) {
	t.Setenv("TASK_FORMAT", "")
	a := newConfigTestApp("", nil)
	a.jsonOut = true

	out, _, err := execConfigCmd(t, a, "tasks")
	if err != nil {
		t.Fatalf("空配置应输出空列表而非报错: %v", err)
	}
	if !strings.Contains(out, `"tasks":[]`) {
		t.Errorf("空配置应输出 []: %s", out)
	}
}

func TestConfigTasksJSONInvalidDSLIsConfigError(t *testing.T) {
	t.Setenv("TASK_FORMAT", "xxx,bad")
	a := newConfigTestApp("", nil)
	a.jsonOut = true

	_, _, err := execConfigCmd(t, a, "tasks")
	if err == nil {
		t.Fatal("无效 DSL 应报错")
	}
	if e := output.Classify(err); e.Type != output.TypeConfig {
		t.Errorf("error.type = %s, want config", e.Type)
	}
}

func TestConfigTasksNonInteractiveWithoutJSON(t *testing.T) {
	a := newConfigTestApp("", nil)
	_, _, err := execConfigCmd(t, a, "tasks")
	if err == nil {
		t.Fatal("非交互且非 --json 应明确报错而非启动编辑器")
	}
	e := output.Classify(err)
	if e.Type != output.TypeValidation || !strings.Contains(e.Hint, "--json") {
		t.Errorf("应归 validation 且 hint 指向 --json: type=%s hint=%q", e.Type, e.Hint)
	}
}
