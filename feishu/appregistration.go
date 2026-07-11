package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 飞书应用注册端点：匿名 OAuth device flow（无需任何预置凭证），
// begin 与 poll 同 URL，靠 form 的 action 字段区分。
//
// 未公开接口，考证自 lark CLI internal/auth/app_registration.go。
const (
	appRegistrationURL     = "https://accounts.feishu.cn/oauth/v1/app/registration"
	defaultOpenBaseURL     = "https://open.feishu.cn"
	registrationArchetype  = "PersonalAgent"
	defaultRegExpiresInSec = 300
	defaultRegIntervalSec  = 5
	maxRegPollIntervalSec  = 60
	defaultMaxPollAttempts = 600
)

var (
	// ErrLarkTenantNotSupported poll 成功但 tenant_brand=lark 且 secret 为空：
	// Lark 海外租户需切 accounts.larksuite.com 重试，room 仅支持飞书域，不支持。
	ErrLarkTenantNotSupported = errors.New("检测到 Lark（海外版）租户：room 仅支持飞书（feishu.cn）域，请改用 room config set 手动配置凭证")
	// ErrRegistrationDenied 用户在授权页拒绝创建应用。
	ErrRegistrationDenied = errors.New("用户拒绝了授权")
	// ErrRegistrationExpired 设备码过期或轮询超时。
	ErrRegistrationExpired = errors.New("设备码已过期或授权超时，请重试")
)

// AppRegistrationCode 注册 begin 的返回（设备码授权入口）。
type AppRegistrationCode struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string // 手动兜底展示
	VerificationURIComplete string
	ExpiresInSec            int // 设备码有效期（秒），缺省 300
	IntervalSec             int // 轮询间隔（秒），缺省 5
}

// AppCredentials 注册 poll 成功的返回（新创建应用的凭证）。
type AppCredentials struct {
	AppID       string // client_id
	AppSecret   string // client_secret
	OpenID      string // user_info.open_id
	TenantBrand string // user_info.tenant_brand（"lark" 已转为错误，此处恒为 "feishu" 或空）
}

// AppRegistrar 匿名 OAuth device flow 客户端：注册飞书 PersonalAgent 个人应用，
// 拿到新应用的 app_id/app_secret（纯裸 HTTP，不依赖 lark SDK）。
type AppRegistrar struct {
	HTTP  *http.Client
	Log   *slog.Logger // debug 只记 endpoint + 状态码，绝不含 secret
	Clock func() time.Time

	// 测试缝隙（生产为零值，走默认）：
	registrationURL string                                     // 注入 httptest 地址
	openBaseURL     string                                     // 仅用于 verification_uri_complete 缺省时拼授权页链接
	sleep           func(context.Context, time.Duration) error // 轮询等待；注入零等待并记录 interval
	maxPollAttempts int                                        // 轮询次数上界；注小值快速触发超时分支
}

type appRegistrationBeginResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
	Error                   string `json:"error"`
	ErrorDescription        string `json:"error_description"`
}

type appRegistrationPollResponse struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	UserInfo     struct {
		OpenID      string `json:"open_id"`
		TenantBrand string `json:"tenant_brand"`
	} `json:"user_info"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// Begin 发起 PersonalAgent 应用注册（device flow 第一步）。
// 匿名请求，无 Authorization 头；archetype=PersonalAgent 即「个人应用」。
func (r *AppRegistrar) Begin(ctx context.Context) (*AppRegistrationCode, error) {
	form := url.Values{}
	form.Set("action", "begin")
	form.Set("archetype", registrationArchetype)
	form.Set("auth_method", "client_secret")
	form.Set("request_user_info", "open_id tenant_brand")

	status, body, err := r.post(ctx, form)
	if err != nil {
		return nil, fmt.Errorf("发起应用注册失败: %w", err)
	}
	var data appRegistrationBeginResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("发起应用注册失败: HTTP %d 响应非 JSON: %s", status, strings.TrimSpace(string(body)))
	}
	if status >= 400 || data.Error != "" {
		return nil, fmt.Errorf("发起应用注册失败: %s", firstNonEmpty(data.ErrorDescription, data.Error, "未知错误"))
	}
	if data.DeviceCode == "" {
		return nil, fmt.Errorf("发起应用注册失败: 响应缺少 device_code，请重试")
	}

	code := &AppRegistrationCode{
		DeviceCode:              data.DeviceCode,
		UserCode:                data.UserCode,
		VerificationURI:         data.VerificationURI,
		VerificationURIComplete: data.VerificationURIComplete,
		ExpiresInSec:            data.ExpiresIn,
		IntervalSec:             data.Interval,
	}
	if code.ExpiresInSec == 0 {
		code.ExpiresInSec = defaultRegExpiresInSec
	}
	if code.IntervalSec == 0 {
		code.IntervalSec = defaultRegIntervalSec
	}
	if code.VerificationURIComplete == "" {
		base := firstNonEmpty(r.openBaseURL, defaultOpenBaseURL)
		// 授权页链接考证自 lark CLI：{open 域}/page/cli?user_code=...
		code.VerificationURIComplete = base + "/page/cli?user_code=" + url.QueryEscape(code.UserCode)
	}
	return code, nil
}

// Poll 轮询注册端点，直至用户完成授权、被拒或超时（device flow 第二步）。
// 每轮先等待 interval 秒；网络/解析错误按瞬时处理并退避重试。
func (r *AppRegistrar) Poll(ctx context.Context, deviceCode string, intervalSec, expiresInSec int) (*AppCredentials, error) {
	if intervalSec < 1 {
		intervalSec = defaultRegIntervalSec
	}
	if expiresInSec <= 0 {
		expiresInSec = defaultRegExpiresInSec
	}

	deadline := r.Clock().Add(time.Duration(expiresInSec) * time.Second)
	interval := intervalSec
	maxAttempts := r.maxPollAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxPollAttempts
	}

	for attempts := 0; r.Clock().Before(deadline) && attempts < maxAttempts; attempts++ {
		if err := waitFor(ctx, r.sleep, time.Duration(interval)*time.Second); err != nil {
			return nil, fmt.Errorf("应用注册轮询已取消: %w", err)
		}

		form := url.Values{}
		form.Set("action", "poll")
		form.Set("device_code", deviceCode)

		_, body, err := r.post(ctx, form)
		if err != nil {
			interval = bumpInterval(interval, 1, maxRegPollIntervalSec)
			continue
		}
		var data appRegistrationPollResponse
		if err := json.Unmarshal(body, &data); err != nil {
			interval = bumpInterval(interval, 1, maxRegPollIntervalSec)
			continue
		}

		if data.Error == "" && data.ClientID != "" {
			if data.ClientSecret == "" {
				if data.UserInfo.TenantBrand == "lark" {
					return nil, ErrLarkTenantNotSupported
				}
				return nil, fmt.Errorf("应用注册响应缺少 client_secret，请重试或改用 room config set 手动配置凭证")
			}
			return &AppCredentials{
				AppID:       data.ClientID,
				AppSecret:   data.ClientSecret,
				OpenID:      data.UserInfo.OpenID,
				TenantBrand: data.UserInfo.TenantBrand,
			}, nil
		}
		// 无 error 也无 client_id（空 JSON、网关 JSON 错误页）：按瞬时故障退避重试，
		// 不作终态——deadline/maxAttempts 兜底，不会死循环
		if data.Error == "" {
			interval = bumpInterval(interval, 1, maxRegPollIntervalSec)
			continue
		}

		switch data.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval = bumpInterval(interval, 5, maxRegPollIntervalSec)
			continue
		case "access_denied":
			return nil, wrapSentinel(ErrRegistrationDenied, data.ErrorDescription)
		case "expired_token", "invalid_grant":
			return nil, wrapSentinel(ErrRegistrationExpired, data.ErrorDescription)
		}
		return nil, fmt.Errorf("应用注册失败: %s", firstNonEmpty(data.ErrorDescription, data.Error, "未知错误"))
	}

	return nil, ErrRegistrationExpired
}

func (r *AppRegistrar) post(ctx context.Context, form url.Values) (int, []byte, error) {
	endpoint := firstNonEmpty(r.registrationURL, appRegistrationURL)
	status, body, err := postForm(ctx, r.HTTP, endpoint, form, "")
	if err != nil {
		r.Log.Debug("应用注册请求失败", "endpoint", endpoint, "error", err)
	} else {
		r.Log.Debug("应用注册请求", "endpoint", endpoint, "status", status)
	}
	return status, body, err
}

// waitFor 等待 d；生产 sleep 为 nil 走 time.After（可被 ctx 取消打断），测试注入 sleep 消除真实等待。
func waitFor(ctx context.Context, sleep func(context.Context, time.Duration) error, d time.Duration) error {
	if sleep != nil {
		return sleep(ctx, d)
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func bumpInterval(cur, delta, max int) int {
	if next := cur + delta; next < max {
		return next
	}
	return max
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func wrapSentinel(base error, detail string) error {
	if detail == "" {
		return base
	}
	return fmt.Errorf("%w: %s", base, detail)
}
