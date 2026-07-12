package feishu

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	deviceAuthURL  = "https://accounts.feishu.cn/oauth/v1/device_authorization"
	oauthTokenURL  = "https://open.feishu.cn/open-apis/authen/v2/oauth/token"
	tenantAuthURL  = "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal"
	oauthRevokeURL = "https://accounts.feishu.cn/oauth/v1/revoke"
	offlineAccess  = "offline_access"
)

// DeviceAuthorization 设备授权响应。
type DeviceAuthorization struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresInSec            int
	IntervalSec             int
}

// UserTokenResult OAuth token 端点返回的用户凭证。
type UserTokenResult struct {
	AccessToken         string
	AccessExpiresInSec  int
	RefreshToken        string
	RefreshExpiresInSec int
	Scope               string
}

// DevicePollStatus 设备码轮询状态。
type DevicePollStatus int

const (
	PollSuccess DevicePollStatus = iota
	PollPending
	PollSlowDown
)

// DevicePollResult 设备码轮询结果。
type DevicePollResult struct {
	Status DevicePollStatus
	Token  *UserTokenResult
}

// TokenClient 跨边界的飞书鉴权网络客户端抽象，便于测试注入 fake。
type TokenClient interface {
	VerifyTenantCredentials(ctx context.Context) error
	RequestDeviceAuthorization(ctx context.Context, scope string) (*DeviceAuthorization, error)
	PollDeviceToken(ctx context.Context, deviceCode string) (*DevicePollResult, error)
	RefreshUserToken(ctx context.Context, refreshToken string) (*UserTokenResult, error)
	GetUserInfo(ctx context.Context, accessToken string) (*UserIdentity, error)
}

// OAuthClient TokenClient 的 HTTP 实现。
type OAuthClient struct {
	HTTP      *http.Client
	AppID     string
	AppSecret string

	revokeURL   string // 测试缝隙：空走 oauthRevokeURL
	userInfoURL string // 测试缝隙：空走 authUserInfoURL
}

// RevokeToken 撤销一枚已签发的 OAuth token（best-effort 登出/换应用用）。
// POST accounts 域 /oauth/v1/revoke，form-encoded，无 Authorization 头；
// tokenTypeHint 取 "refresh_token" 或 "access_token"。
func (c *OAuthClient) RevokeToken(ctx context.Context, token, tokenTypeHint string) error {
	form := url.Values{}
	form.Set("client_id", c.AppID)
	form.Set("client_secret", c.AppSecret)
	form.Set("token", token)
	if tokenTypeHint != "" {
		form.Set("token_type_hint", tokenTypeHint)
	}

	endpoint := c.revokeURL
	if endpoint == "" {
		endpoint = oauthRevokeURL
	}
	status, body, err := postForm(ctx, c.HTTP, endpoint, form, "")
	if err != nil {
		return fmt.Errorf("revoke 请求失败: %w", err)
	}
	if status >= 400 {
		return fmt.Errorf("revoke 失败: HTTP %d: %s", status, strings.TrimSpace(string(body)))
	}
	// 飞书业务错误：HTTP 200 但 body 里 code != 0
	if len(body) > 0 {
		var data struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if err := json.Unmarshal(body, &data); err == nil && data.Code != 0 {
			return fmt.Errorf("revoke 失败 [%d]: %s", data.Code, data.Msg)
		}
	}
	return nil
}

// VerifyTenantCredentials 显式获取一次 tenant_access_token 以校验应用凭据
// （业务调用的 tenant token 由 SDK 自动管理）。
func (c *OAuthClient) VerifyTenantCredentials(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"app_id": c.AppID, "app_secret": c.AppSecret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tenantAuthURL, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	var resp struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if err := c.doJSON(req, &resp); err != nil {
		return err
	}
	if resp.Code != 0 {
		return &APIError{Code: resp.Code, Msg: resp.Msg}
	}
	if resp.TenantAccessToken == "" {
		return fmt.Errorf("API 响应中缺少 tenant_access_token 字段")
	}
	return nil
}

func (c *OAuthClient) RequestDeviceAuthorization(ctx context.Context, scope string) (*DeviceAuthorization, error) {
	fullScope := scope
	if !strings.Contains(scope, offlineAccess) {
		fullScope = strings.TrimSpace(scope + " " + offlineAccess)
	}

	form := url.Values{"client_id": {c.AppID}, "scope": {fullScope}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deviceAuthURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	basic := base64.StdEncoding.EncodeToString([]byte(c.AppID + ":" + c.AppSecret))
	req.Header.Set("Authorization", "Basic "+basic)

	var data struct {
		Error                   string `json:"error"`
		ErrorDescription        string `json:"error_description"`
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := c.doJSON(req, &data); err != nil {
		return nil, err
	}
	if data.Error != "" || data.DeviceCode == "" {
		return nil, fmt.Errorf("请求设备授权失败: %s - %s", data.Error, data.ErrorDescription)
	}

	auth := &DeviceAuthorization{
		DeviceCode:              data.DeviceCode,
		UserCode:                data.UserCode,
		VerificationURI:         data.VerificationURI,
		VerificationURIComplete: data.VerificationURIComplete,
		ExpiresInSec:            data.ExpiresIn,
		IntervalSec:             data.Interval,
	}
	if auth.ExpiresInSec == 0 {
		auth.ExpiresInSec = 300
	}
	if auth.IntervalSec == 0 {
		auth.IntervalSec = 5
	}
	return auth, nil
}

func (c *OAuthClient) PollDeviceToken(ctx context.Context, deviceCode string) (*DevicePollResult, error) {
	form := url.Values{
		"grant_type":    {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code":   {deviceCode},
		"client_id":     {c.AppID},
		"client_secret": {c.AppSecret},
	}
	data, err := c.postOAuthToken(ctx, form)
	if err != nil {
		return nil, err
	}

	switch data.Error {
	case "authorization_pending":
		return &DevicePollResult{Status: PollPending}, nil
	case "slow_down":
		return &DevicePollResult{Status: PollSlowDown}, nil
	}
	if data.Error != "" || data.AccessToken == "" {
		return nil, fmt.Errorf("设备码授权失败: %s - %s", data.Error, data.ErrorDescription)
	}
	return &DevicePollResult{Status: PollSuccess, Token: data.toResult("")}, nil
}

func (c *OAuthClient) RefreshUserToken(ctx context.Context, refreshToken string) (*UserTokenResult, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {c.AppID},
		"client_secret": {c.AppSecret},
	}
	data, err := c.postOAuthToken(ctx, form)
	if err != nil {
		return nil, err
	}
	if data.Error != "" || data.AccessToken == "" {
		return nil, fmt.Errorf("刷新用户 access_token 失败: %s - %s", data.Error, data.ErrorDescription)
	}
	return data.toResult(refreshToken), nil
}

// oauthTokenData v2 OAuth token 端点响应；错误信息随非 2xx 状态返回，一并按 JSON 解析。
type oauthTokenData struct {
	Error                 string `json:"error"`
	ErrorDescription      string `json:"error_description"`
	AccessToken           string `json:"access_token"`
	ExpiresIn             int    `json:"expires_in"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
	RefreshExpiresIn      int    `json:"refresh_expires_in"`
	Scope                 string `json:"scope"`
}

func (d *oauthTokenData) toResult(fallbackRefreshToken string) *UserTokenResult {
	result := &UserTokenResult{
		AccessToken:         d.AccessToken,
		AccessExpiresInSec:  d.ExpiresIn,
		RefreshToken:        d.RefreshToken,
		RefreshExpiresInSec: d.RefreshTokenExpiresIn,
		Scope:               d.Scope,
	}
	if result.AccessExpiresInSec == 0 {
		result.AccessExpiresInSec = 7200
	}
	if result.RefreshToken == "" {
		result.RefreshToken = fallbackRefreshToken
	}
	if result.RefreshExpiresInSec == 0 {
		result.RefreshExpiresInSec = d.RefreshExpiresIn
	}
	if result.RefreshExpiresInSec == 0 {
		result.RefreshExpiresInSec = 30 * 24 * 3600
	}
	return result
}

func (c *OAuthClient) postOAuthToken(ctx context.Context, form url.Values) (*oauthTokenData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var data oauthTokenData
	if err := c.doJSON(req, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

func (c *OAuthClient) doJSON(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("解析响应失败 (HTTP %d): %w", resp.StatusCode, err)
	}
	return nil
}

// ParseAuthExpireAt 从 access_token（JWT）中解出授权硬顶 auth_exp，转为绝对毫秒时间戳。
// auth_exp = 首次授权时刻 + 1 年，刷新不会延长。非 JWT / 缺字段 / 解析失败时返回 0。
func ParseAuthExpireAt(accessToken string) int64 {
	parts := strings.Split(accessToken, ".")
	if len(parts) < 2 || parts[1] == "" {
		return 0
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var claims struct {
		AuthExp int64 `json:"auth_exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0
	}
	return claims.AuthExp * 1000
}
