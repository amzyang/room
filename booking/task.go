// Package booking 会议室预订编排：任务解析、房间选择、重叠检测与日程管理。
package booking

import (
	"strconv"
	"strings"
	"time"

	"github.com/amzyang/room/envutil"
)

const defaultFrequencyInterval = 1

// Task TASK_FORMAT 中的一条预订任务。
type Task struct {
	Title        string
	DayOfWeek    string
	StartTime    string
	EndTime      string
	Frequency    string
	Interval     int
	StartDate    string // 周期锚点日期（YYYY-MM-DD），空表示无锚点
	Participants []string
}

var weekMap = map[string]time.Weekday{
	"mon": time.Monday,
	"tue": time.Tuesday,
	"wed": time.Wednesday,
	"thu": time.Thursday,
	"fri": time.Friday,
	"sat": time.Saturday,
	"sun": time.Sunday,
}

// ParseParticipants 拆分参与者串：全角空格归一化后按冒号与空白分隔。
func ParseParticipants(s string) []string {
	s = strings.ReplaceAll(s, "　", " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var participants []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ':' || r == ' ' || r == '\t' || r == '\n'
	}) {
		if part != "" {
			participants = append(participants, part)
		}
	}
	return participants
}

// ParseTaskFormat 解析 TASK_FORMAT DSL：
// dayOfWeek,startTime-endTime,frequency[:interval[:startDate]],participants,title，多任务用 | 分隔。
func ParseTaskFormat(taskFormat string) []Task {
	var tasks []Task
	for _, taskStr := range strings.Split(taskFormat, "|") {
		if strings.TrimSpace(taskStr) == "" {
			continue
		}
		parts := strings.Split(taskStr, ",")
		for i, part := range parts {
			parts[i] = envutil.CleanEnvValue(part)
		}
		for len(parts) < 5 {
			parts = append(parts, "")
		}
		dayOfWeek, timeRange, frequencyConfig, participants, title := parts[0], parts[1], parts[2], parts[3], parts[4]

		startTime, endTime, _ := strings.Cut(timeRange, "-")

		frequency := frequencyConfig
		interval := defaultFrequencyInterval
		startDate := ""
		if strings.Contains(frequencyConfig, ":") {
			freqParts := strings.Split(frequencyConfig, ":")
			frequency = freqParts[0]
			if len(freqParts) >= 2 {
				if parsed, err := strconv.Atoi(freqParts[1]); err == nil && parsed >= 1 {
					interval = parsed
				}
			}
			if len(freqParts) >= 3 {
				if _, err := time.Parse("2006-01-02", freqParts[2]); err == nil {
					startDate = freqParts[2]
				}
			}
		}

		participantList := ParseParticipants(participants)

		tasks = append(tasks, Task{
			Title:        title,
			DayOfWeek:    dayOfWeek,
			StartTime:    strings.TrimSpace(startTime),
			EndTime:      strings.TrimSpace(endTime),
			Frequency:    frequency,
			Interval:     interval,
			StartDate:    startDate,
			Participants: participantList,
		})
	}
	return tasks
}

// IsDayOfWeekMatch 日期是否命中任务的星期几配置。
func IsDayOfWeekMatch(date time.Time, dayOfWeek string) bool {
	target, ok := weekMap[strings.ToLower(dayOfWeek)]
	return ok && date.Weekday() == target
}

// IsInCycle 日期是否落在任务的重复周期上（相对锚点 startDate 按 interval 计算）。
// 无锚点/无频率配置时恒为 true。
func IsInCycle(date time.Time, task Task, loc *time.Location) bool {
	if task.StartDate == "" || task.Frequency == "" || task.Interval == 0 {
		return true
	}
	startDate, err := time.ParseInLocation("2006-01-02", task.StartDate, loc)
	if err != nil {
		return true
	}

	// 对齐 dayjs diff 语义：毫秒差换算后向零取整
	daysDiff := int(date.Sub(startDate).Hours() / 24)

	switch strings.ToLower(task.Frequency) {
	case "daily":
		return daysDiff >= 0 && daysDiff%task.Interval == 0
	case "weekly":
		weeksDiff := floorDiv(daysDiff, 7)
		return weeksDiff >= 0 && weeksDiff%task.Interval == 0
	case "monthly":
		monthsDiff := monthDiff(startDate, date)
		return monthsDiff >= 0 && monthsDiff%task.Interval == 0
	default:
		return true
	}
}

// floorDiv 向下取整除法（对齐 JS Math.floor(a/b)，Go 原生整除为向零取整）。
func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// monthDiff 整月差（对齐 dayjs diff(month) 语义：不足整月的部分舍去）。
func monthDiff(start, end time.Time) int {
	months := (end.Year()-start.Year())*12 + int(end.Month()) - int(start.Month())
	anchor := start.AddDate(0, months, 0)
	if end.Before(anchor) {
		months--
	}
	return months
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
