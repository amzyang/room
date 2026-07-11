package feishu

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// WebhookTextBody 自定义机器人文本消息体；开启「签名校验」时附带 timestamp + sign。
type WebhookTextBody struct {
	MsgType   string             `json:"msg_type"`
	Content   WebhookTextContent `json:"content"`
	Timestamp string             `json:"timestamp,omitempty"`
	Sign      string             `json:"sign,omitempty"`
}

type WebhookTextContent struct {
	Text string `json:"text"`
}

// webhookResponse code=0（或老格式 StatusCode=0）表示成功。
type webhookResponse struct {
	Code          *int   `json:"code"`
	Msg           string `json:"msg"`
	StatusCode    *int   `json:"StatusCode"`
	StatusMessage string `json:"StatusMessage"`
}

// SignWebhook 飞书自定义机器人加签：以 "{timestampSec}\n{secret}" 为 key，
// 对空串做 HMAC-SHA256 后 base64。
func SignWebhook(secret string, timestampSec int64) string {
	key := strconv.FormatInt(timestampSec, 10) + "\n" + secret
	mac := hmac.New(sha256.New, []byte(key))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// BuildTextBody 构造文本消息体；secret 非空时按 timestampSec 加签。
func BuildTextBody(text, secret string, timestampSec int64) WebhookTextBody {
	body := WebhookTextBody{MsgType: "text", Content: WebhookTextContent{Text: text}}
	if secret != "" {
		body.Timestamp = strconv.FormatInt(timestampSec, 10)
		body.Sign = SignWebhook(secret, timestampSec)
	}
	return body
}

// WebhookPoster HTTP 边界抽象，便于测试注入内存实现。
type WebhookPoster func(url string, body WebhookTextBody) (code int, msg string, err error)

// WebhookClient 飞书自定义机器人客户端。仅需 webhook 地址（可选签名密钥），
// 不依赖应用/租户凭证。
type WebhookClient struct {
	URL    string
	Secret string
	Post   WebhookPoster
	Clock  func() time.Time
}

// NewWebhookPoster 基于共享 HTTP 客户端构建默认 Poster。
func NewWebhookPoster(httpClient *http.Client) WebhookPoster {
	return func(url string, body WebhookTextBody) (int, string, error) {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, "", err
		}
		resp, err := httpClient.Post(url, "application/json", bytes.NewReader(payload))
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()

		var result webhookResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return 0, "", err
		}
		switch {
		case result.Code != nil:
			return *result.Code, result.Msg, nil
		case result.StatusCode != nil:
			return *result.StatusCode, result.StatusMessage, nil
		default:
			return 0, "", nil
		}
	}
}

// SendText 发送文本消息。
func (c *WebhookClient) SendText(text string) error {
	timestampSec := c.Clock().Unix()
	body := BuildTextBody(text, c.Secret, timestampSec)
	code, msg, err := c.Post(c.URL, body)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("webhook 发送失败: code=%d msg=%s", code, msg)
	}
	return nil
}
