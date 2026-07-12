package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrType 错误类型闭集，与退出码及信封 error.type 字段一一对应。
type ErrType string

const (
	TypeAPI                  ErrType = "api"
	TypeValidation           ErrType = "validation"
	TypeAuth                 ErrType = "auth"
	TypeConfig               ErrType = "config"
	TypeConflict             ErrType = "conflict"
	TypeNoRoom               ErrType = "no_room"
	TypeHolidaySkipped       ErrType = "holiday_skipped"
	TypeConfirmationRequired ErrType = "confirmation_required"
	TypeInternal             ErrType = "internal"
)

// Error 类型化错误：Message 说明是什么问题，Hint 给下一步命令式指引。
type Error struct {
	Type      ErrType `json:"type"`
	Message   string  `json:"message"`
	Hint      string  `json:"hint,omitempty"`
	Retryable bool    `json:"retryable,omitempty"`
	Detail    any     `json:"detail,omitempty"`
	cause     error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.cause }

// WithRetryable 标记该错误可原样重试（如设备码过期）。
func (e *Error) WithRetryable() *Error {
	e.Retryable = true
	return e
}

// WithDetail 附加结构化上下文（如 book 失败时的 date/time）。
func (e *Error) WithDetail(d any) *Error {
	e.Detail = d
	return e
}

// Errf 构造类型化错误。
func Errf(t ErrType, hint, format string, args ...any) *Error {
	return &Error{Type: t, Message: fmt.Sprintf(format, args...), Hint: hint}
}

// Wrap 包装底层错误为类型化错误，保留错误链供 sentry 上报。
func Wrap(t ErrType, hint string, cause error) *Error {
	return &Error{Type: t, Message: cause.Error(), Hint: hint, cause: cause}
}

// Classify 把任意 error 归一为 *Error：已类型化的原样返回，其余兜底归 api
// （room 的未分类错误几乎都来自飞书 API 侧）。
func Classify(err error) *Error {
	var oe *Error
	if errors.As(err, &oe) {
		return oe
	}
	return &Error{Type: TypeAPI, Message: err.Error(), cause: err}
}

// Reportable 该错误是否值得上报遥测：业务结果（no_room/conflict/holiday_skipped）
// 与用户侧问题（validation/auth/config/confirmation_required）不是缺陷，
// 仅 api/internal（含未分类兜底）需要关注。
func Reportable(err error) bool {
	switch Classify(err).Type {
	case TypeAPI, TypeInternal:
		return true
	}
	return false
}

// WriteError 渲染错误：asJSON 时写一行 {ok:false,error:{...}} 信封，
// 否则写「错误: … / 提示: …」人类可读格式。
func WriteError(w io.Writer, err error, asJSON bool) {
	e := Classify(err)
	if asJSON {
		_ = json.NewEncoder(w).Encode(struct {
			OK    bool   `json:"ok"`
			Error *Error `json:"error"`
		}{false, e})
		return
	}
	fmt.Fprintf(w, "错误: %s\n", e.Message)
	if e.Hint != "" {
		fmt.Fprintf(w, "提示: %s\n", e.Hint)
	}
}
