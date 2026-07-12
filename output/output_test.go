package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestWriteSuccess(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteSuccess(&buf, map[string]string{"status": "booked"}, &Meta{Count: 1}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Error("信封应以换行结尾（NDJSON）")
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("非法 JSON: %v: %s", err, buf.String())
	}
	if got["ok"] != true {
		t.Errorf("ok = %v, want true", got["ok"])
	}
	if got["data"].(map[string]any)["status"] != "booked" {
		t.Errorf("data.status = %v", got["data"])
	}
	if got["meta"].(map[string]any)["count"] != float64(1) {
		t.Errorf("meta.count = %v", got["meta"])
	}
}

func TestWriteSuccessNoMeta(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteSuccess(&buf, map[string]string{"k": "v"}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "meta") {
		t.Errorf("nil meta 不应出现在信封: %s", buf.String())
	}
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, ExitOK},
		{"validation", Errf(TypeValidation, "", "bad input"), ExitValidation},
		{"auth", Errf(TypeAuth, "", "no token"), ExitAuth},
		{"config", Errf(TypeConfig, "", "missing key"), ExitAuth},
		{"confirmation", Errf(TypeConfirmationRequired, "加 --yes", "need confirm"), ExitConfirmationRequired},
		{"api", Errf(TypeAPI, "", "api down"), ExitAPI},
		{"conflict", Errf(TypeConflict, "", "overlap"), ExitAPI},
		{"no_room", Errf(TypeNoRoom, "", "no room"), ExitAPI},
		{"holiday", Errf(TypeHolidaySkipped, "", "holiday"), ExitAPI},
		{"no_participants", Errf(TypeNoParticipants, "", "no participants"), ExitAPI},
		{"internal", Errf(TypeInternal, "", "boom"), ExitAPI},
		{"未分类兜底为 api", errors.New("plain"), ExitAPI},
		{"包装链中识别类型", fmt.Errorf("outer: %w", Errf(TypeValidation, "", "inner")), ExitValidation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestReportable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"api 上报", Errf(TypeAPI, "", "api down"), true},
		{"internal 上报", Errf(TypeInternal, "", "boom"), true},
		{"未分类兜底上报", errors.New("plain"), true},
		{"validation 不上报", Errf(TypeValidation, "", "bad"), false},
		{"config 不上报", Errf(TypeConfig, "", "missing"), false},
		{"auth 不上报", Errf(TypeAuth, "", "expired"), false},
		{"confirmation 不上报", Errf(TypeConfirmationRequired, "", "need yes"), false},
		{"no_room 业务结果不上报", Errf(TypeNoRoom, "", "no room"), false},
		{"conflict 业务结果不上报", Errf(TypeConflict, "", "overlap"), false},
		{"holiday 业务结果不上报", Errf(TypeHolidaySkipped, "", "holiday"), false},
		{"no_participants 业务结果不上报", Errf(TypeNoParticipants, "", "no participants"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Reportable(tt.err); got != tt.want {
				t.Errorf("Reportable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClassifyPreservesTypedError(t *testing.T) {
	orig := Errf(TypeConfig, "run room init", "missing app_id")
	if got := Classify(orig); got != orig {
		t.Error("已类型化错误应原样返回")
	}
	plain := errors.New("boom")
	got := Classify(plain)
	if got.Type != TypeAPI {
		t.Errorf("未分类错误应归 api, got %s", got.Type)
	}
	if !errors.Is(got, plain) {
		t.Error("Classify 应保留错误链")
	}
}

func TestWrapPreservesCause(t *testing.T) {
	cause := errors.New("network down")
	e := Wrap(TypeAPI, "稍后重试", cause)
	if !errors.Is(e, cause) {
		t.Error("Wrap 应保留错误链")
	}
	if e.Error() != "network down" {
		t.Errorf("Message 应取自 cause: %s", e.Error())
	}
}

func TestWriteErrorJSON(t *testing.T) {
	var buf bytes.Buffer
	err := Errf(TypeNoRoom, "更换时间段重试", "未找到可用会议室").
		WithDetail(map[string]string{"date": "2026-07-15"}).WithRetryable()
	WriteError(&buf, err, true)

	var got struct {
		OK    bool `json:"ok"`
		Error struct {
			Type      string         `json:"type"`
			Message   string         `json:"message"`
			Hint      string         `json:"hint"`
			Retryable bool           `json:"retryable"`
			Detail    map[string]any `json:"detail"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("非法 JSON: %v: %s", err, buf.String())
	}
	if got.OK {
		t.Error("错误信封 ok 应为 false")
	}
	if got.Error.Type != "no_room" || got.Error.Hint == "" || !got.Error.Retryable {
		t.Errorf("错误信封字段不符: %+v", got.Error)
	}
	if got.Error.Detail["date"] != "2026-07-15" {
		t.Errorf("detail 丢失: %+v", got.Error.Detail)
	}
}

func TestWriteErrorHuman(t *testing.T) {
	var buf bytes.Buffer
	WriteError(&buf, Errf(TypeConfig, "运行 room init", "缺失 app_id"), false)
	out := buf.String()
	if !strings.Contains(out, "错误: 缺失 app_id") || !strings.Contains(out, "提示: 运行 room init") {
		t.Errorf("人类格式不符:\n%s", out)
	}

	buf.Reset()
	WriteError(&buf, errors.New("plain"), false)
	if strings.Contains(buf.String(), "提示") {
		t.Errorf("无 hint 时不应打印提示行:\n%s", buf.String())
	}
}
