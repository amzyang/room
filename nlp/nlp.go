// Package nlp 通过 OpenAI 兼容接口把自然语言会议描述解析为结构化预订信息。
package nlp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	// DefaultMeetingDuration 默认会议时长（分钟）。
	DefaultMeetingDuration = 60
	// DefaultAPIBase 默认 OpenAI 兼容 API 地址。
	DefaultAPIBase = "https://api.openai.com/v1"
	// DefaultModel 默认模型。
	DefaultModel = "claude-4.6-opus"
)

// ParsedOneShotInfo 一次性解析出的完整预订信息。
type ParsedOneShotInfo struct {
	Date         string   `json:"date"`
	StartTime    string   `json:"startTime"`
	EndTime      string   `json:"endTime"`
	Duration     int      `json:"duration"`
	Participants []string `json:"participants"`
	Title        string   `json:"title"`
}

// PastTimeChoice 过去时间的处理选择。
type PastTimeChoice string

const (
	ChoiceTomorrow PastTimeChoice = "tomorrow"
	ChoiceNextWeek PastTimeChoice = "nextWeek"
	ChoiceCancel   PastTimeChoice = "cancel"
)

// IsPastMeetingTime 会议开始时间是否早于当前时间。
func IsPastMeetingTime(date, startTime string, now time.Time, loc *time.Location) bool {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", date+" "+startTime, loc)
	if err != nil {
		return false
	}
	return t.Before(now)
}

// ShiftMeetingDate 按选择推迟会议日期；cancel 返回空串。
func ShiftMeetingDate(date string, choice PastTimeChoice, loc *time.Location) string {
	if choice == ChoiceCancel {
		return ""
	}
	days := 1
	if choice == ChoiceNextWeek {
		days = 7
	}
	t, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return ""
	}
	return t.AddDate(0, 0, days).Format("2006-01-02")
}

// Parser 自然语言解析器；APIKey 为空时不可用（上层回退交互式输入）。
type Parser struct {
	APIKey  string
	BaseURL string
	Model   string
	HTTP    *http.Client
	Log     *slog.Logger
	Clock   func() time.Time
	Loc     *time.Location
}

func (p *Parser) Available() bool {
	return p.APIKey != ""
}

var weekdayNames = map[time.Weekday]string{
	time.Sunday:    "Sunday",
	time.Monday:    "Monday",
	time.Tuesday:   "Tuesday",
	time.Wednesday: "Wednesday",
	time.Thursday:  "Thursday",
	time.Friday:    "Friday",
	time.Saturday:  "Saturday",
}

// ParseOneShotWithDefaults 解析并填充默认值：无 endTime 时按 duration
// （缺省 60 分钟）计算。
func (p *Parser) ParseOneShotWithDefaults(ctx context.Context, input string) (*ParsedOneShotInfo, error) {
	parsed, err := p.ParseOneShotRequest(ctx, input)
	if err != nil {
		return nil, err
	}

	if parsed.EndTime == "" {
		duration := parsed.Duration
		if duration == 0 {
			duration = DefaultMeetingDuration
			parsed.Duration = duration
			p.Log.Info(fmt.Sprintf("未指定时长，使用默认值: %d分钟", DefaultMeetingDuration))
		}
		start, err := time.ParseInLocation("2006-01-02 15:04:05", parsed.Date+" "+parsed.StartTime, p.Loc)
		if err != nil {
			return nil, fmt.Errorf("invalid startTime format: %w", err)
		}
		parsed.EndTime = start.Add(time.Duration(duration) * time.Minute).Format("15:04:05")
	}
	return parsed, nil
}

// ParseOneShotRequest 调用 LLM 将自然语言解析为 JSON 结构。
func (p *Parser) ParseOneShotRequest(ctx context.Context, input string) (*ParsedOneShotInfo, error) {
	if !p.Available() {
		return nil, fmt.Errorf("OpenAI client not initialized - missing API key")
	}

	now := p.Clock().In(p.Loc)
	today := now.Format("2006-01-02")
	todayWeekday := weekdayNames[now.Weekday()]
	currentTime := now.Format("15:04")

	prompt := fmt.Sprintf(`
你是一个会议安排助手，需要从用户的自然语言输入中提取完整的会议预订信息，包括时间、参与者和标题。

当前时间信息：
- 今天是：%s (%s)
- 当前时间：%s

请从以下用户输入中提取会议信息：
"%s"

请返回JSON格式的结果，包含以下字段：
- date: 会议日期 (YYYY-MM-DD格式)
- startTime: 开始时间 (HH:MM:SS格式)
- endTime: 结束时间 (HH:MM:SS格式，如果无法确定则为null)
- duration: 会议时长(分钟，如果无法确定则为null)
- participants: 参与者列表 (字符串数组，如果没有则为空数组[])
- title: 会议标题 (字符串，如果没有明确提及则为null)

解析规则：
1. 时间解析：
   - 如果用户未指定具体日期，使用今天的日期
   - 支持相对时间："明天"、"下周一"、"next monday"、"tom"等
   - 支持缩写："mon/tue/wed/thu/fri/sat/sun"
   - 支持简写时间："6pm"→18:00，"10am"→10:00
   - 支持时长表达："30mins"、"1h"、"90mins"等
   - 如果只有开始时间和时长，计算结束时间
2. 参与者解析：
   - 识别人名（拼音或中文），如 "shikai"、"zhangsan"、"李四"
   - 识别邮箱前缀，如 "wangwu"
   - 识别飞书群ID，如 "oc_xxxxx"
   - 参与者通常出现在时间信息之后，用空格或逗号分隔
   - 不要将时间相关的词（如 "am"、"pm"、"h"、"min"）误识别为参与者
3. 标题解析：
   - 只有用户明确提到会议主题/标题时才提取，如"团队周会"、"code review"
   - 如果没有明确提及，title设为null

示例输入输出：
- "10am, 1h, shikai zhangsan" → {"date": "%s", "startTime": "10:00:00", "endTime": "11:00:00", "duration": 60, "participants": ["shikai", "zhangsan"], "title": null}
- "3pm" → {"date": "%s", "startTime": "15:00:00", "endTime": null, "duration": null, "participants": [], "title": null}
- "团队周会 9am 30min lisi" → {"date": "%s", "startTime": "09:00:00", "endTime": "09:30:00", "duration": 30, "participants": ["lisi"], "title": "团队周会"}
- "tom 2pm 1h wangwu oc_abc123" → {"date": "明天日期", "startTime": "14:00:00", "endTime": "15:00:00", "duration": 60, "participants": ["wangwu", "oc_abc123"], "title": null}

重要格式要求：
- 直接返回有效的JSON对象，不要使用任何markdown格式
- 不要在JSON前后添加`+"```json或```"+`标记
- 不要添加任何解释性文字或注释
- 响应必须是可以直接JSON.parse()的字符串
`, today, todayWeekday, currentTime, input, today, today, today)

	content, err := p.chatCompletion(ctx, prompt)
	if err != nil {
		return nil, err
	}
	p.Log.Info(fmt.Sprintf("OpenAI response: %s", content))

	cleaned := StripCodeFence(content)

	var parsed ParsedOneShotInfo
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		return nil, fmt.Errorf("解析 LLM 响应失败: %w", err)
	}

	if parsed.Date == "" || parsed.StartTime == "" {
		return nil, fmt.Errorf("invalid parsed result: missing date or startTime")
	}
	if _, err := time.ParseInLocation("2006-01-02", parsed.Date, p.Loc); err != nil {
		return nil, fmt.Errorf("invalid date format")
	}
	if _, err := time.ParseInLocation("2006-01-02 15:04:05", parsed.Date+" "+parsed.StartTime, p.Loc); err != nil {
		return nil, fmt.Errorf("invalid startTime format")
	}
	if parsed.EndTime != "" {
		if _, err := time.ParseInLocation("2006-01-02 15:04:05", parsed.Date+" "+parsed.EndTime, p.Loc); err != nil {
			return nil, fmt.Errorf("invalid endTime format")
		}
	}
	return &parsed, nil
}

// StripCodeFence 剥离 LLM 响应中可能包裹的 markdown 代码围栏。
func StripCodeFence(content string) string {
	cleaned := strings.TrimSpace(content)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	return strings.TrimSpace(cleaned)
}

func (p *Parser) chatCompletion(ctx context.Context, prompt string) (string, error) {
	payload := map[string]any{
		"model": p.Model,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "你是一个专业的会议安排助手，擅长从自然语言中提取时间、参与者和会议标题信息。必须严格按照要求返回纯JSON格式，不使用markdown代码块或任何格式化标记。",
			},
			{"role": "user", "content": prompt},
		},
		"temperature": 0.1,
		"max_tokens":  500,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	url := strings.TrimSuffix(p.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)

	resp, err := p.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("解析 chat completions 响应失败 (HTTP %d): %w", resp.StatusCode, err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("chat completions 错误: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("no response from OpenAI")
	}
	return result.Choices[0].Message.Content, nil
}
