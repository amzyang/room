package nlp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

var shanghai, _ = time.LoadLocation("Asia/Shanghai")

func TestStripCodeFence(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"a":1}`, `{"a":1}`},
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{"```\n{\"a\":1}\n```", `{"a":1}`},
		{"  {\"a\":1}  ", `{"a":1}`},
	}
	for _, c := range cases {
		if got := StripCodeFence(c.in); got != c.want {
			t.Errorf("StripCodeFence(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsPastMeetingTime(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, shanghai)
	if !IsPastMeetingTime("2026-07-11", "11:00:00", now, shanghai) {
		t.Error("earlier time should be past")
	}
	if IsPastMeetingTime("2026-07-11", "13:00:00", now, shanghai) {
		t.Error("later time should not be past")
	}
}

func TestShiftMeetingDate(t *testing.T) {
	if got := ShiftMeetingDate("2026-07-11", ChoiceTomorrow, shanghai); got != "2026-07-12" {
		t.Errorf("tomorrow = %q", got)
	}
	if got := ShiftMeetingDate("2026-07-11", ChoiceNextWeek, shanghai); got != "2026-07-18" {
		t.Errorf("nextWeek = %q", got)
	}
	if got := ShiftMeetingDate("2026-07-11", ChoiceCancel, shanghai); got != "" {
		t.Errorf("cancel = %q, want empty", got)
	}
}

func newTestParser(t *testing.T, llmResponse string) *Parser {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": llmResponse}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(server.Close)

	return &Parser{
		APIKey:  "test-key",
		BaseURL: server.URL,
		Model:   "test-model",
		HTTP:    server.Client(),
		Log:     slog.New(slog.DiscardHandler),
		Clock:   func() time.Time { return time.Date(2026, 7, 11, 12, 0, 0, 0, shanghai) },
		Loc:     shanghai,
	}
}

func TestParseOneShotWithDefaults(t *testing.T) {
	// markdown 围栏 + endTime 缺失 + duration 存在 → 计算 endTime
	parser := newTestParser(t, "```json\n"+
		`{"date":"2026-07-12","startTime":"14:00:00","endTime":null,"duration":90,"participants":["shikai"],"title":null}`+
		"\n```")
	parsed, err := parser.ParseOneShotWithDefaults(context.Background(), "tom 2pm 90mins shikai")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.EndTime != "15:30:00" {
		t.Errorf("endTime = %q, want 15:30:00", parsed.EndTime)
	}

	// endTime 与 duration 均缺失 → 默认 60 分钟
	parser = newTestParser(t,
		`{"date":"2026-07-12","startTime":"14:00:00","endTime":null,"duration":null,"participants":[],"title":null}`)
	parsed, err = parser.ParseOneShotWithDefaults(context.Background(), "tom 2pm")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.EndTime != "15:00:00" || parsed.Duration != DefaultMeetingDuration {
		t.Errorf("endTime = %q duration = %d, want 15:00:00 / 60", parsed.EndTime, parsed.Duration)
	}

	// 非法日期 → 报错
	parser = newTestParser(t,
		`{"date":"not-a-date","startTime":"14:00:00","endTime":null,"duration":null,"participants":[],"title":null}`)
	if _, err = parser.ParseOneShotWithDefaults(context.Background(), "x"); err == nil {
		t.Error("invalid date should error")
	}

	// 缺 startTime → 报错
	parser = newTestParser(t, `{"date":"2026-07-12","participants":[]}`)
	if _, err = parser.ParseOneShotWithDefaults(context.Background(), "x"); err == nil {
		t.Error("missing startTime should error")
	}
}

func TestParserUnavailable(t *testing.T) {
	parser := &Parser{Log: slog.New(slog.DiscardHandler), Loc: shanghai, Clock: time.Now}
	if parser.Available() {
		t.Error("parser without API key should be unavailable")
	}
	if _, err := parser.ParseOneShotRequest(context.Background(), "x"); err == nil {
		t.Error("unavailable parser should error")
	}
	_ = fmt.Sprint()
}
