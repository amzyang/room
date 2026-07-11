package feishu

import "fmt"

// 飞书错误码：事件已被删除/取消。
const CodeEventDeleted = 193003

// APIError 飞书开放平台业务错误（envelope code != 0）。
type APIError struct {
	Code int
	Msg  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("飞书API错误 %d: %s", e.Code, e.Msg)
}

// IsEventDeleted 判断错误是否为「事件已被取消/删除」（code 193003）。
func IsEventDeleted(err error) bool {
	apiErr, ok := err.(*APIError)
	return ok && apiErr.Code == CodeEventDeleted
}
