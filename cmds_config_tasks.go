package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/amzyang/room/booking"
	"github.com/amzyang/room/config"
	"github.com/amzyang/room/output"
)

// taskEditorUI 任务编辑器对交互控件的最小依赖（huh 适配或测试脚本）。
// 各方法第二个返回值 false 表示用户取消当前步骤（Esc）。
type taskEditorUI interface {
	menu(title, desc string, options []string) (int, bool, error)
	taskForm(in taskFormInput, multiDay bool) (taskFormInput, bool, error)
	confirm(preview string) (bool, error)
}

// taskFormInput 任务表单的字符串态输入（normalizeTaskInput 规范化前）。
type taskFormInput struct {
	Days         []string // 星期几（mon..sun），编辑时单元素
	TimeRange    string   // 开始-结束，HH:MM 或 HH:MM:SS
	Frequency    string   // weekly / daily / monthly
	Interval     string   // 空 = 1
	StartDate    string   // 空或 YYYY-MM-DD
	Participants string
	Title        string
}

type taskMenuAction int

const (
	taskMenuTask taskMenuAction = iota // 选中一条任务，进入操作子菜单
	taskMenuAdd
	taskMenuSave
	taskMenuQuit
)

type taskMenuOption struct {
	Label  string
	Action taskMenuAction
	Index  int // 仅 taskMenuTask：任务下标
}

const taskMenuDesc = "自动预订任务会在 room auto 运行时批量创建飞书日程；改动在「保存并退出」后才写入配置"

// 任务操作子菜单：下标即动作。
var taskOpLabels = []string{"编辑", "复制为新任务", "删除", "返回"}

const (
	taskOpEdit = iota
	taskOpCopy
	taskOpDelete
)

// buildTaskMenu 纯函数：任务列表 → 主菜单项。空列表也保留「保存并退出」，
// 删光已有任务后靠它把清空写回配置。
func buildTaskMenu(tasks []booking.Task) []taskMenuOption {
	var opts []taskMenuOption
	for i, t := range tasks {
		opts = append(opts, taskMenuOption{Label: booking.DescribeTask(t), Action: taskMenuTask, Index: i})
	}
	return append(opts,
		taskMenuOption{Label: "➕ 新增任务", Action: taskMenuAdd},
		taskMenuOption{Label: "💾 保存并退出", Action: taskMenuSave},
		taskMenuOption{Label: "✕ 放弃修改", Action: taskMenuQuit},
	)
}

func taskMenuTitle(n int) string {
	if n == 0 {
		return "booking.task_format · 未配置"
	}
	return fmt.Sprintf("booking.task_format · %d 个任务", n)
}

// manageTasks 编辑器主循环：任务 CRUD 全部发生在内存，返回（结果，是否保存）。
// 主菜单 Esc 等同「放弃修改」，子步骤 Esc 只回到主菜单。
func manageTasks(tasks []booking.Task, ui taskEditorUI, now time.Time, loc *time.Location) ([]booking.Task, bool, error) {
	for {
		opts := buildTaskMenu(tasks)
		labels := make([]string, len(opts))
		for i, o := range opts {
			labels[i] = o.Label
		}
		idx, ok, err := ui.menu(taskMenuTitle(len(tasks)), taskMenuDesc, labels)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		switch opt := opts[idx]; opt.Action {
		case taskMenuSave:
			return tasks, true, nil
		case taskMenuQuit:
			return nil, false, nil
		case taskMenuAdd:
			added, err := taskFormFlow(ui, defaultTaskFormInput(), true, now, loc)
			if err != nil {
				return nil, false, err
			}
			tasks = append(tasks, added...)
		case taskMenuTask:
			if tasks, err = taskOpFlow(tasks, opt.Index, ui, now, loc); err != nil {
				return nil, false, err
			}
		}
	}
}

// taskOpFlow 单条任务的操作子菜单。
func taskOpFlow(tasks []booking.Task, i int, ui taskEditorUI, now time.Time, loc *time.Location) ([]booking.Task, error) {
	op, ok, err := ui.menu(booking.DescribeTask(tasks[i]), "", taskOpLabels)
	if err != nil || !ok {
		return tasks, err
	}
	switch op {
	case taskOpEdit:
		edited, err := taskFormFlow(ui, taskToFormInput(tasks[i]), false, now, loc)
		if err != nil {
			return nil, err
		}
		if len(edited) == 1 {
			tasks[i] = edited[0]
		}
	case taskOpCopy:
		added, err := taskFormFlow(ui, taskToFormInput(tasks[i]), true, now, loc)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, added...)
	case taskOpDelete:
		tasks = append(tasks[:i], tasks[i+1:]...)
	}
	return tasks, nil
}

// taskFormFlow 表单 → 规范化 → 预览确认；表单取消或预览拒绝返回 nil（不改动）。
func taskFormFlow(ui taskEditorUI, in taskFormInput, multiDay bool, now time.Time, loc *time.Location) ([]booking.Task, error) {
	filled, ok, err := ui.taskForm(in, multiDay)
	if err != nil || !ok {
		return nil, err
	}
	tasks := normalizeTaskInput(filled, now, loc)
	yes, err := ui.confirm(previewTasks(tasks, now, loc))
	if err != nil || !yes {
		return nil, err
	}
	return tasks, nil
}

func defaultTaskFormInput() taskFormInput {
	return taskFormInput{Frequency: "weekly", Interval: "1"}
}

// taskToFormInput 任务 → 表单预填（编辑/复制入口）。
func taskToFormInput(t booking.Task) taskFormInput {
	freq := strings.ToLower(t.Frequency)
	if freq == "" {
		freq = "weekly"
	}
	return taskFormInput{
		Days:         []string{strings.ToLower(t.DayOfWeek)},
		TimeRange:    t.StartTime + "-" + t.EndTime,
		Frequency:    freq,
		Interval:     strconv.Itoa(t.Interval),
		StartDate:    t.StartDate,
		Participants: strings.Join(t.Participants, " "),
		Title:        t.Title,
	}
}

// normalizeTaskInput 已校验的表单输入 → 任务列表：星期多选展开为多条；
// HH:MM 补秒为 HH:MM:SS（BookRoom 的时间格式）；间隔>1 未填锚点时自动取
// 该星期几的下一个命中日，否则间隔在运行时不生效（IsInCycle 无锚点恒真）。
func normalizeTaskInput(in taskFormInput, now time.Time, loc *time.Location) []booking.Task {
	start, end, _ := strings.Cut(in.TimeRange, "-")
	interval := 1
	if v, err := strconv.Atoi(strings.TrimSpace(in.Interval)); err == nil {
		interval = v
	}
	var tasks []booking.Task
	for _, day := range in.Days {
		t := booking.Task{
			DayOfWeek:    day,
			StartTime:    normalizeClock(start),
			EndTime:      normalizeClock(end),
			Frequency:    in.Frequency,
			Interval:     interval,
			StartDate:    strings.TrimSpace(in.StartDate),
			Participants: booking.ParseParticipants(in.Participants),
			Title:        strings.TrimSpace(in.Title),
		}
		if t.Interval > 1 && t.StartDate == "" {
			anchor := t
			anchor.Interval, anchor.StartDate = 1, ""
			t.StartDate = booking.NextOccurrences(anchor, now, 1, loc)[0].Format("2006-01-02")
		}
		tasks = append(tasks, t)
	}
	return tasks
}

// normalizeClock HH:MM → HH:MM:SS。
func normalizeClock(s string) string {
	s = strings.TrimSpace(s)
	if strings.Count(s, ":") == 1 {
		return s + ":00"
	}
	return s
}

// previewTasks 保存前预览：人话摘要 + 生成的 DSL + 接下来的命中日期。
func previewTasks(tasks []booking.Task, now time.Time, loc *time.Location) string {
	var b strings.Builder
	fmt.Fprintf(&b, "将保存 %d 条任务：\n", len(tasks))
	for _, t := range tasks {
		fmt.Fprintf(&b, "\n%s\nDSL: %s\n", booking.DescribeTask(t), booking.FormatTask(t))
		hits := booking.NextOccurrences(t, now, 3, loc)
		if len(hits) == 0 {
			b.WriteString("接下来命中：无（检查星期几与锚点日期）\n")
			continue
		}
		dates := make([]string, len(hits))
		for i, h := range hits {
			dates[i] = h.Format("2006-01-02")
		}
		fmt.Fprintf(&b, "接下来命中：%s\n", strings.Join(dates, "、"))
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---- 表单字段校验（huh 适配挂载，入口层校验一次，后续信任） ----

func validateTaskDays(days []string) error {
	if len(days) == 0 {
		return fmt.Errorf("至少选择一个星期几")
	}
	return nil
}

func parseClock(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse("15:04:05", s); err == nil {
		return t, nil
	}
	return time.Parse("15:04", s)
}

func validateTaskTimeRange(s string) error {
	start, end, ok := strings.Cut(s, "-")
	if !ok {
		return fmt.Errorf("需为 开始-结束（如 10:00-11:30）")
	}
	st, err := parseClock(start)
	if err != nil {
		return fmt.Errorf("开始时间 %q 无效（如 10:00）", strings.TrimSpace(start))
	}
	et, err := parseClock(end)
	if err != nil {
		return fmt.Errorf("结束时间 %q 无效（如 11:30）", strings.TrimSpace(end))
	}
	if !st.Before(et) {
		return fmt.Errorf("开始时间需早于结束时间")
	}
	return nil
}

func validateTaskInterval(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || v < 1 {
		return fmt.Errorf("需为 ≥1 的整数（2=隔周）")
	}
	return nil
}

func validateTaskDate(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", strings.TrimSpace(s)); err != nil {
		return fmt.Errorf("需为 YYYY-MM-DD（如 2026-07-01）")
	}
	return nil
}

// validateNoDSLChars DSL 无转义语法，字段值不能含分隔符。
func validateNoDSLChars(s string) error {
	if strings.ContainsAny(s, ",|") {
		return fmt.Errorf("不能包含半角逗号或竖线（可用中文逗号「，」）")
	}
	return nil
}

func validateTaskParticipants(s string) error {
	return validateNoDSLChars(s)
}

func validateTaskTitle(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("标题必填（写入飞书日程）")
	}
	return validateNoDSLChars(s)
}

// ---- huh 适配（机械翻译，不进测试） ----

type taskEditorHuh struct{}

var taskWeekOptions = []huh.Option[string]{
	huh.NewOption("周一", "mon"), huh.NewOption("周二", "tue"), huh.NewOption("周三", "wed"),
	huh.NewOption("周四", "thu"), huh.NewOption("周五", "fri"), huh.NewOption("周六", "sat"),
	huh.NewOption("周日", "sun"),
}

func (taskEditorHuh) menu(title, desc string, options []string) (int, bool, error) {
	idx := 0
	opts := make([]huh.Option[int], len(options))
	for i, l := range options {
		opts[i] = huh.NewOption(l, i)
	}
	sel := huh.NewSelect[int]().Title(title).Description(desc).Options(opts...).Value(&idx)
	if err := runHuhForm(huh.NewGroup(sel)); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return idx, true, nil
}

func (taskEditorHuh) taskForm(in taskFormInput, multiDay bool) (taskFormInput, bool, error) {
	day := ""
	if len(in.Days) > 0 {
		day = in.Days[0]
	}
	var dayField huh.Field
	if multiDay {
		dayField = huh.NewMultiSelect[string]().Title("星期几").
			Description("空格勾选；多选一次生成多条任务").
			Options(taskWeekOptions...).Value(&in.Days).Validate(validateTaskDays)
	} else {
		dayField = huh.NewSelect[string]().Title("星期几").
			Options(taskWeekOptions...).Value(&day)
	}
	err := runHuhForm(huh.NewGroup(
		dayField,
		huh.NewInput().Title("时间段（24 小时制）").Placeholder("10:00-11:30").
			Description("每次预订的开始-结束时间").
			Value(&in.TimeRange).Validate(validateTaskTimeRange),
		huh.NewSelect[string]().Title("频率").
			Description("周期的计算单位；只有命中上面星期几的日期才会预订").
			Options(
				huh.NewOption("每周 weekly", "weekly"),
				huh.NewOption("每天 daily", "daily"),
				huh.NewOption("每月 monthly", "monthly"),
			).Value(&in.Frequency),
		huh.NewInput().Title("间隔").Placeholder("1").
			Description("每 N 个周期预订一次：1=每周，2=隔周；留空为 1").
			Value(&in.Interval).Validate(validateTaskInterval),
		huh.NewInput().Title("锚点日期（可留空）").Placeholder("2026-07-01").
			Description("从这天起才开始预订；间隔>1 未填时自动取下一个命中日").
			Value(&in.StartDate).Validate(validateTaskDate),
		huh.NewInput().Title("参与者（可留空）").Placeholder("zhangsan lisi").
			Description("邮箱前缀或 oc_ 群聊 ID，空格分隔；room login 的授权用户会自动加入").
			Value(&in.Participants).Validate(validateTaskParticipants),
		huh.NewInput().Title("标题").Placeholder("周会").
			Description("写入飞书日程的标题").
			Value(&in.Title).Validate(validateTaskTitle),
	))
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return in, false, nil
		}
		return in, false, err
	}
	if !multiDay {
		in.Days = []string{day}
	}
	return in, true, nil
}

func (taskEditorHuh) confirm(preview string) (bool, error) {
	yes := true
	err := runHuhForm(huh.NewGroup(
		huh.NewNote().Title("预览").Description(preview),
		huh.NewConfirm().Title("确认？").Affirmative("保存").Negative("返回修改").Value(&yes),
	))
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, nil
		}
		return false, err
	}
	return yes, nil
}

func runHuhForm(groups ...*huh.Group) error {
	return huh.NewForm(groups...).WithAccessible(os.Getenv("ACCESSIBLE") != "").Run()
}

// runConfigTasksEditor booking.task_format 的交互式任务编辑器（纯本地，无需飞书凭证）。
func runConfigTasksEditor(cmd *cobra.Command, a *app) error {
	if !a.interactive() {
		return output.Errf(output.TypeValidation,
			"非交互环境请用 room config set booking.task_format VALUE 直接写入（语法见 README）",
			"任务编辑器需要在终端中交互运行")
	}
	tasks := booking.CanonicalizeTasks(booking.ParseTaskFormat(a.cfg.Entries["TASK_FORMAT"].Value))
	result, save, err := manageTasks(tasks, taskEditorHuh{}, a.now(), a.loc)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if !save {
		fmt.Fprintln(out, "已取消，未保存")
		return nil
	}
	it, _ := config.ByEnvKey("TASK_FORMAT")
	if len(result) == 0 {
		doc, err := config.ReadFile(a.cfg.Path)
		if err != nil {
			return fmt.Errorf("读取 %s 失败: %w", a.cfg.Path, err)
		}
		delete(doc.Values, it.EnvKey)
		if err := config.WriteFile(a.cfg.Path, doc); err != nil {
			return err
		}
		fmt.Fprintf(out, "已清空 %s: booking.task_format（room auto 将不执行任何任务）\n", a.cfg.Path)
		warnOverride(cmd, a, it)
		return nil
	}
	if _, err := setConfigValue(a, it, booking.FormatTasks(result)); err != nil {
		return err
	}
	fmt.Fprintf(out, "已写入 %s: booking.task_format（%d 个任务）\n", a.cfg.Path, len(result))
	fmt.Fprintln(out, "提示: 运行 room auto --dryrun 可预览实际预订计划")
	warnOverride(cmd, a, it)
	return nil
}
