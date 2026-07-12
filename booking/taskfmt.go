package booking

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/amzyang/room/envutil"
)

// occurrenceScanLimit NextOccurrences 的逐日扫描上限（天），
// 防止锚点在遥远未来时长循环。
const occurrenceScanLimit = 550

var validFrequencies = map[string]bool{"weekly": true, "daily": true, "monthly": true}

var weekLabel = map[string]string{
	"mon": "周一", "tue": "周二", "wed": "周三", "thu": "周四",
	"fri": "周五", "sat": "周六", "sun": "周日",
}

// FormatTask 把任务序列化回 TASK_FORMAT DSL 的一条：
// dayOfWeek,startTime-endTime,frequency[:interval[:startDate]],participants,title。
// interval=1 且无锚点时省略后缀，保持 DSL 简短。
func FormatTask(t Task) string {
	freq := t.Frequency
	if t.StartDate != "" {
		freq = fmt.Sprintf("%s:%d:%s", t.Frequency, t.Interval, t.StartDate)
	} else if t.Interval > 1 {
		freq = fmt.Sprintf("%s:%d", t.Frequency, t.Interval)
	}
	fields := []string{
		t.DayOfWeek,
		t.StartTime + "-" + t.EndTime,
		freq,
		strings.Join(t.Participants, ":"),
		t.Title,
	}
	return strings.Join(fields, ",")
}

// FormatTasks 多条任务用 | 连接，与 ParseTaskFormat 互逆。
func FormatTasks(tasks []Task) string {
	lines := make([]string, len(tasks))
	for i, t := range tasks {
		lines[i] = FormatTask(t)
	}
	return strings.Join(lines, "|")
}

// DescribeTask 任务的人话摘要（TUI 菜单/预览用），如
// 「周三 10:00–11:30 · 每2周（自2026-07-01） · 周会 · alice bob」。
func DescribeTask(t Task) string {
	day := t.DayOfWeek
	if label, ok := weekLabel[strings.ToLower(day)]; ok {
		day = label
	}
	parts := []string{
		day + " " + displayTime(t.StartTime) + "–" + displayTime(t.EndTime),
		describeFrequency(t),
	}
	title := t.Title
	if title == "" {
		title = "（无标题）"
	}
	parts = append(parts, title)
	if len(t.Participants) > 0 {
		parts = append(parts, strings.Join(t.Participants, " "))
	}
	return strings.Join(parts, " · ")
}

// describeFrequency 频率的人话描述。空频率运行时按周命中（星期几过滤主导），故也显示「每周」。
func describeFrequency(t Task) string {
	unit := map[string]string{"weekly": "周", "daily": "天", "monthly": "月", "": "周"}[strings.ToLower(t.Frequency)]
	if unit == "" {
		unit = t.Frequency
	}
	s := "每" + unit
	if t.Interval > 1 {
		s = fmt.Sprintf("每%d%s", t.Interval, unit)
	}
	if t.StartDate != "" {
		s += fmt.Sprintf("（自%s）", t.StartDate)
	}
	return s
}

// displayTime HH:MM:SS 整分时间省略秒（仅展示用，序列化保持原值）。
func displayTime(s string) string {
	if len(s) == 8 && strings.HasSuffix(s, ":00") {
		return s[:5]
	}
	return s
}

// NextOccurrences 从 from（含当日）起最多 n 个命中日期，
// 与 AutoBook/processTask 相同的逐日匹配语义（IsDayOfWeekMatch + IsInCycle）。
func NextOccurrences(t Task, from time.Time, n int, loc *time.Location) []time.Time {
	var out []time.Time
	date := from.In(loc)
	for i := 0; i < occurrenceScanLimit && len(out) < n; i++ {
		if IsDayOfWeekMatch(date, t.DayOfWeek) && IsInCycle(date, t, loc) {
			out = append(out, date)
		}
		date = date.AddDate(0, 0, 1)
	}
	return out
}

// CanonicalizeTasks 历史配置归一：无锚点的间隔运行时被 IsInCycle 忽略（恒按每周期
// 命中），归一为 1，使 DescribeTask/FormatTasks 如实反映运行时行为，编辑器保存
// 不被 ValidateTaskFormat 拦下用户未改动的旧任务。
func CanonicalizeTasks(tasks []Task) []Task {
	for i, t := range tasks {
		if t.Interval > 1 && t.StartDate == "" {
			tasks[i].Interval = 1
		}
	}
	return tasks
}

// ValidateTaskFormat 整段 TASK_FORMAT DSL 的严格校验，供配置表单在写入前拦截
// ParseTaskFormat 会静默降级、运行时必然不工作的输入。空串（未配置）合法。
func ValidateTaskFormat(s string) error {
	n := 0
	for _, taskStr := range strings.Split(s, "|") {
		if strings.TrimSpace(taskStr) == "" {
			continue
		}
		n++
		if err := validateTaskFields(taskStr); err != nil {
			return fmt.Errorf("第 %d 条任务: %w", n, err)
		}
	}
	return nil
}

func validateTaskFields(taskStr string) error {
	parts := strings.Split(taskStr, ",")
	if len(parts) > 5 {
		return fmt.Errorf("字段多于 5 个，标题不能含半角逗号（可用中文逗号「，」）")
	}
	for i, p := range parts {
		parts[i] = envutil.CleanEnvValue(p)
	}
	for len(parts) < 5 {
		parts = append(parts, "")
	}
	if _, ok := weekMap[strings.ToLower(parts[0])]; !ok {
		return fmt.Errorf("星期几 %q 无效（可用 mon/tue/wed/thu/fri/sat/sun）", parts[0])
	}
	if err := validateTimeRange(parts[1]); err != nil {
		return err
	}
	return validateFrequencySpec(parts[2])
}

func validateTimeRange(timeRange string) error {
	start, end, ok := strings.Cut(timeRange, "-")
	if !ok {
		return fmt.Errorf("时间段 %q 无效，需为 开始-结束（如 10:00:00-11:30:00）", timeRange)
	}
	startT, err := time.Parse("15:04:05", strings.TrimSpace(start))
	if err != nil {
		return fmt.Errorf("开始时间 %q 无效，需为 HH:MM:SS（如 10:00:00）", strings.TrimSpace(start))
	}
	endT, err := time.Parse("15:04:05", strings.TrimSpace(end))
	if err != nil {
		return fmt.Errorf("结束时间 %q 无效，需为 HH:MM:SS（如 11:30:00）", strings.TrimSpace(end))
	}
	if !startT.Before(endT) {
		return fmt.Errorf("开始时间 %s 需早于结束时间 %s", strings.TrimSpace(start), strings.TrimSpace(end))
	}
	return nil
}

func validateFrequencySpec(spec string) error {
	if spec == "" {
		return nil
	}
	freqParts := strings.Split(spec, ":")
	if len(freqParts) > 3 {
		return fmt.Errorf("频率 %q 无效，最多为 频率:间隔:锚点 三段（如 weekly:2:2026-07-01）", spec)
	}
	if !validFrequencies[strings.ToLower(freqParts[0])] {
		return fmt.Errorf("频率 %q 无效（可用 weekly/daily/monthly）", freqParts[0])
	}
	interval := 1
	if len(freqParts) >= 2 {
		parsed, err := strconv.Atoi(freqParts[1])
		if err != nil || parsed < 1 {
			return fmt.Errorf("间隔 %q 无效，需为 ≥1 的整数", freqParts[1])
		}
		interval = parsed
	}
	if len(freqParts) >= 3 {
		if _, err := time.Parse("2006-01-02", freqParts[2]); err != nil {
			return fmt.Errorf("锚点日期 %q 无效，需为 YYYY-MM-DD（如 2026-07-01）", freqParts[2])
		}
	} else if interval > 1 {
		return fmt.Errorf("间隔 %d 需要锚点日期才生效（如 %s:%d:2026-07-01）", interval, freqParts[0], interval)
	}
	return nil
}
