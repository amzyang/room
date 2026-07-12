package feishu

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
)

// AuthMode 鉴权模式：auto（用户优先、应用兜底，默认）/ user（强制用户身份）/ tenant（强制应用身份）。
type AuthMode string

const (
	AuthModeAuto   AuthMode = "auto"
	AuthModeUser   AuthMode = "user"
	AuthModeTenant AuthMode = "tenant"
)

// Auth 管理用户凭证与日历操作的身份决策。
// 业务调用的 tenant_access_token 由 SDK 自动管理，这里只负责用户身份的获取/刷新/降级。
type Auth struct {
	Mode        AuthMode
	TokenClient TokenClient
	Store       UserTokenStore
	Clock       func() time.Time
	Log         *slog.Logger

	// 进程内一次决策后缓存的日历鉴权 option，保证同一次运行内身份一致
	calendarOptions []larkcore.RequestOptionFunc
	decided         bool
}

// UserAccessToken 返回有效的用户 access_token；无凭证 / refresh_token 失效 / 刷新失败时返回空串，
// 交由上层回退应用身份（静默降级，不报错）。
func (a *Auth) UserAccessToken(ctx context.Context) string {
	stored := a.Store.Read()
	if stored == nil {
		return ""
	}

	now := a.Clock()
	if IsAccessTokenValid(stored, now) {
		a.warnIfAuthNearExpiry(stored, now)
		return stored.AccessToken
	}

	if !IsRefreshTokenValid(stored, now) {
		a.Log.Warn("用户凭证已过期(refresh_token 失效)，需重新运行 login，本次回退应用身份")
		return ""
	}

	a.Log.Info("用户 access_token 过期，尝试用 refresh_token 刷新")
	result, err := a.TokenClient.RefreshUserToken(ctx, stored.RefreshToken)
	if err != nil {
		a.Log.Error(fmt.Sprintf("刷新用户 access_token 失败，回退应用身份: %v", err))
		return ""
	}
	updated := a.Persist(result, stored)
	a.warnIfAuthNearExpiry(updated, now)
	a.Log.Info("成功刷新用户 access_token")
	return updated.AccessToken
}

// CalendarAuthOptions 日历类身份敏感操作的统一鉴权入口：进程内只做一次「用户优先、应用兜底」
// 决策并缓存，保证 primary → createEvent → addAttendee 全程使用同一身份。
// 返回空 slice 表示应用身份（SDK 自动附加 tenant_access_token）。
func (a *Auth) CalendarAuthOptions(ctx context.Context) ([]larkcore.RequestOptionFunc, error) {
	if a.decided {
		return a.calendarOptions, nil
	}

	if a.Mode != AuthModeTenant {
		if userToken := a.UserAccessToken(ctx); userToken != "" {
			a.Log.Info("本次日历操作使用【用户身份】")
			a.calendarOptions = []larkcore.RequestOptionFunc{larkcore.WithUserAccessToken(userToken)}
			a.decided = true
			return a.calendarOptions, nil
		}
		if a.Mode == AuthModeUser {
			return nil, fmt.Errorf("FEISHU_AUTH_MODE=user 但无法获取用户 access_token，请先运行 `room login`")
		}
	}

	a.Log.Info("本次日历操作使用【应用身份(兜底)】")
	a.calendarOptions = nil
	a.decided = true
	return a.calendarOptions, nil
}

// RefreshIdentity 强制拉取当前授权用户身份并覆盖写入存储（覆盖是关键：
// 换账号重登录时不能残留上一账号身份）。获取失败时清空身份字段并返回 error，
// 由上层降级为警告（登录本身不失败）。
func (a *Auth) RefreshIdentity(ctx context.Context) (*StoredUserToken, error) {
	accessToken := a.UserAccessToken(ctx)
	identity, err := a.TokenClient.GetUserInfo(ctx, accessToken)

	// UserAccessToken 可能刚刷新并持久化了新 token，重新 Read 防止覆盖
	stored := a.Store.Read()
	if stored == nil {
		return nil, fmt.Errorf("无用户凭证，无法获取用户身份")
	}
	if accessToken == "" {
		err = fmt.Errorf("无有效用户 access_token")
	}
	if err != nil {
		stored.OpenID, stored.UserID, stored.Name = "", "", ""
		a.writeStore(stored)
		return stored, err
	}
	stored.OpenID, stored.UserID, stored.Name = identity.OpenID, identity.UserID, identity.Name
	a.writeStore(stored)
	return stored, nil
}

// UserIdentity 已登录用户身份。优先读存储中已持久化的身份；身份为空（旧凭证）
// 且能取得有效 access_token 时 lazy backfill（调 user_info 并回写存储）。
// 无凭证 / 回填失败返回 nil（仅打 warn，不报错）。
func (a *Auth) UserIdentity(ctx context.Context) *UserIdentity {
	stored := a.Store.Read()
	if stored == nil {
		return nil
	}
	if stored.OpenID != "" || stored.UserID != "" {
		return &UserIdentity{OpenID: stored.OpenID, UserID: stored.UserID, Name: stored.Name}
	}

	accessToken := a.UserAccessToken(ctx)
	if accessToken == "" {
		return nil
	}
	identity, err := a.TokenClient.GetUserInfo(ctx, accessToken)
	if err != nil {
		a.Log.Warn(fmt.Sprintf("获取用户身份失败: %v", err))
		return nil
	}
	// 回写前重新 Read：UserAccessToken 可能刚刷新并持久化了新 token
	if cur := a.Store.Read(); cur != nil {
		cur.OpenID, cur.UserID, cur.Name = identity.OpenID, identity.UserID, identity.Name
		a.writeStore(cur)
	}
	return identity
}

func (a *Auth) writeStore(token *StoredUserToken) {
	if err := a.Store.Write(token); err != nil {
		a.Log.Error(fmt.Sprintf("保存用户凭证失败: %v", err))
	}
}

// Persist 将 token 结果写入存储；prev 用于保留旧凭证中的补充字段。
func (a *Auth) Persist(result *UserTokenResult, prev *StoredUserToken) *StoredUserToken {
	now := a.Clock()
	stored := &StoredUserToken{
		AccessToken:     result.AccessToken,
		AccessExpireAt:  now.UnixMilli() + int64(result.AccessExpiresInSec)*1000,
		RefreshToken:    result.RefreshToken,
		RefreshExpireAt: now.UnixMilli() + int64(result.RefreshExpiresInSec)*1000,
		AuthExpireAt:    ParseAuthExpireAt(result.AccessToken),
		Scope:           result.Scope,
	}
	if prev != nil {
		if stored.AuthExpireAt == 0 {
			stored.AuthExpireAt = prev.AuthExpireAt
		}
		if stored.Scope == "" {
			stored.Scope = prev.Scope
		}
		stored.OpenID = prev.OpenID
		stored.UserID = prev.UserID
		stored.Name = prev.Name
	}
	if err := a.Store.Write(stored); err != nil {
		a.Log.Error(fmt.Sprintf("保存用户凭证失败: %v", err))
	}
	return stored
}

// 授权硬顶(auth_exp)临近/已过时提醒重新授权；auth_exp 刷新不会延长，cron 场景尤其需要。
func (a *Auth) warnIfAuthNearExpiry(token *StoredUserToken, now time.Time) {
	if !IsAuthNearExpiry(token, now) {
		return
	}
	remainDays := (token.AuthExpireAt - now.UnixMilli()) / (24 * 60 * 60 * 1000)
	if remainDays > 0 {
		a.Log.Warn(fmt.Sprintf("用户授权将于约 %d 天后到期(auth_exp 硬顶，刷新不会延长)，请尽快重新运行 login 重新授权", remainDays))
	} else {
		a.Log.Warn("用户授权已到期(auth_exp)，请重新运行 login 重新授权，否则将回退应用身份")
	}
}
