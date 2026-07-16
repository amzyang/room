package booking

import (
	"fmt"
	"strings"
	"time"
)

// HasTimeOverlap 判断两个时间段是否重叠；端点相接（end1==start2 或 end2==start1）不算重叠。
func HasTimeOverlap(start1, end1, start2, end2 time.Time) bool {
	if end1.Equal(start2) || end2.Equal(start1) {
		return false
	}
	between := func(t, lo, hi time.Time) bool {
		return !t.Before(lo) && !t.After(hi)
	}
	return between(start1, start2, end2) || between(end1, start2, end2) ||
		between(start2, start1, end1) || between(end2, start1, end1)
}

// IsOrganizedBy 判断事件是否由指定日历（当前用户主日历）组织。
// organizer_calendar_id 缺失时返回 false —— 无法确认是本人组织，保守排除。
func IsOrganizedBy(organizerCalendarID, myCalendarID string) bool {
	return organizerCalendarID != "" && organizerCalendarID == myCalendarID
}

// EventSummary 事件列表项（list/cancel 命令展示与 --json 输出用）。
type EventSummary struct {
	EventID        string `json:"event_id"`
	Title          string `json:"title"`
	Description    string `json:"description,omitempty"`
	StartTime      string `json:"start_time"`
	EndTime        string `json:"end_time"`
	Location       string `json:"location,omitempty"`
	StartTimestamp int64  `json:"start_timestamp"`
	EndTimestamp   int64  `json:"end_timestamp"`
}

// FormatEventList list 命令输出（不含行尾空格）。scope 为范围描述
// （如「未来 7 天内」「2026-07-20 当天」），mineOnly 为 true 时注明仅你组织的。
func FormatEventList(events []EventSummary, scope string, mineOnly bool) string {
	kind, of := "日历事件", "的"
	if mineOnly {
		kind, of = "你组织的日历事件", ""
	}
	if len(events) == 0 {
		return fmt.Sprintf("%s没有%s\n", scope, kind)
	}
	header := fmt.Sprintf("\n%s%s%s（共 %d 个）:", scope, of, kind, len(events))
	return formatEvents(header, events)
}

// FormatCancelList cancel 命令的可取消事件列表输出。
func FormatCancelList(events []EventSummary, days int) string {
	if len(events) == 0 {
		return fmt.Sprintf("未来 %d 天内没有你可取消的事件（仅列出你组织的）\n", days)
	}
	header := fmt.Sprintf("\n未来 %d 天可取消的事件（仅你组织的）:", days)
	return formatEvents(header, events)
}

func formatEvents(header string, events []EventSummary) string {
	var b strings.Builder
	b.WriteString(header + "\n")
	b.WriteString(strings.Repeat("=", 80) + "\n")
	for i, event := range events {
		fmt.Fprintf(&b, "%d. %s\n", i+1, event.Title)
		fmt.Fprintf(&b, "   时间: %s - %s\n", event.StartTime, event.EndTime)
		fmt.Fprintf(&b, "   地点: %s\n", locationOrNone(event.Location))
		if event.Description != "" {
			fmt.Fprintf(&b, "   描述: %s\n", event.Description)
		}
		fmt.Fprintf(&b, "   事件ID: %s\n", event.EventID)
		b.WriteString("\n")
	}
	return b.String()
}

func locationOrNone(location string) string {
	if location == "" {
		return "无"
	}
	return location
}
