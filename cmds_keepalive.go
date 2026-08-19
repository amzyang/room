package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/amzyang/room/feishu"
	"github.com/amzyang/room/output"
)

// keepaliveData keepalive --json 的 data 载荷（时间为 epoch ms，与 whoami 对齐）。
type keepaliveData struct {
	Event             string `json:"event"` // keepalive_ok
	RefreshExpireAtMs int64  `json:"refresh_expire_at_ms"`
	AuthExpireAtMs    int64  `json:"auth_expire_at_ms,omitempty"`
	AuthNearExpiry    bool   `json:"auth_near_expiry,omitempty"`
}

func newKeepaliveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "keepalive",
		Short: "静默刷新用户凭证，滚动 refresh 窗口（配合定时任务防低频使用掉线）",
		Long: `无条件刷新一次用户凭证并保存，把约 7 天的 refresh 滑动窗口重新拉满。

飞书用户授权的 refresh_token 闲置约 7 天即失效，低频使用会掉线；
定时（如每 2 天）运行本命令可将登录态一直续到授权硬顶（首次登录起一年）。
推荐经 brew services start room 托管，由 launchd 定时执行。

刷新失败时本地凭证保持原样（可稍后重试）；refresh 窗口已断或授权到
一年硬顶时需重新 room login。`,
		Example: `  room keepalive
  room keepalive --json
  brew services start room`,
		Args: validationArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			token := a.userTokens().Read()
			now := a.now()
			if token == nil {
				return output.Errf(output.TypeAuth, "运行 room login 完成用户授权", "未登录")
			}
			if token.AuthExpireAt > 0 && now.UnixMilli() >= token.AuthExpireAt {
				return output.Errf(output.TypeAuth,
					"重新运行 room login（授权自首次登录起一年到期，刷新不延长）", "登录授权已到一年硬顶")
			}
			if !feishu.IsRefreshTokenValid(token, now) {
				return output.Errf(output.TypeAuth, "重新运行 room login", "刷新凭证已过期")
			}
			auth := a.keepaliveAuth()
			if auth == nil {
				return errMissingAppCreds()
			}
			refreshed, err := auth.ForceRefresh(cmd.Context())
			if err != nil {
				return output.Wrap(output.TypeAPI,
					"本地凭证保持原样，可稍后重试；若持续失败重新运行 room login", err).WithRetryable()
			}
			nearExpiry := feishu.IsAuthNearExpiry(refreshed, now)
			if a.jsonOut {
				return output.WriteSuccess(cmd.OutOrStdout(), keepaliveData{
					Event: "keepalive_ok", RefreshExpireAtMs: refreshed.RefreshExpireAt,
					AuthExpireAtMs: refreshed.AuthExpireAt, AuthNearExpiry: nearExpiry,
				}, nil)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "凭证已刷新，刷新窗口滚动至 %s\n", fmtExpiry(refreshed.RefreshExpireAt, now, a.loc))
			if refreshed.AuthExpireAt > 0 {
				suffix := ""
				if nearExpiry {
					suffix = "，请尽快重新 room login"
				}
				fmt.Fprintf(w, "授权硬顶: %s（刷新不延长%s）\n", fmtExpiry(refreshed.AuthExpireAt, now, a.loc), suffix)
			}
			return nil
		},
	}
}

// keepaliveAuth 组装保活用 Auth；tokenClient 为测试缝隙，生产按应用凭证构造。
// 缺应用凭证返回 nil（refresh 端点需要 client_id/client_secret）。
func (a *app) keepaliveAuth() *feishu.Auth {
	client := a.tokenClient
	if client == nil {
		oauthClient := envOAuthClient()
		if oauthClient == nil {
			return nil
		}
		client = oauthClient
	}
	return &feishu.Auth{
		Mode: feishu.AuthModeAuto, TokenClient: client,
		Store: a.userTokens(), Clock: a.now, Log: a.log,
	}
}
