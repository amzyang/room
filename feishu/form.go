package feishu

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// postForm 发送 application/x-www-form-urlencoded POST，返回状态码与完整响应体。
// basicAuth 非空时设置 Authorization: Basic 头。供 AppRegistrar 与 RevokeToken 共用。
func postForm(ctx context.Context, client *http.Client, endpoint string, form url.Values, basicAuth string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if basicAuth != "" {
		req.Header.Set("Authorization", "Basic "+basicAuth)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}
