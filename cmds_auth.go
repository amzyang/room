package main

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/amzyang/room/feishu"
	"github.com/amzyang/room/output"
)

// userTokenStore whoami/logout 依赖的本地凭证能力（测试注入内存实现）。
// 含 Write 以满足 feishu.UserTokenStore，logout 的远端撤销直接复用 store。
type userTokenStore interface {
	Read() *feishu.StoredUserToken
	Write(token *feishu.StoredUserToken) error
	Delete() error
}

// userTokens 凭证存储缝隙：测试注入 fake，生产懒初始化为本地文件存储。
func (a *app) userTokens() userTokenStore {
	if a.tokenStore == nil {
		a.tokenStore = &feishu.FileUserTokenStore{Path: userTokenPath()}
	}
	return a.tokenStore
}

// whoamiData whoami --json 的 data 载荷（时间为 epoch ms，与 login_ok 事件对齐）。
type whoamiData struct {
	LoggedIn          bool   `json:"logged_in"`
	AppID             string `json:"app_id,omitempty"`
	Name              string `json:"name,omitempty"`
	OpenID            string `json:"open_id,omitempty"`
	UserID            string `json:"user_id,omitempty"`
	Scope             string `json:"scope,omitempty"`
	AccessExpireAtMs  int64  `json:"access_expire_at_ms,omitempty"`
	RefreshExpireAtMs int64  `json:"refresh_expire_at_ms,omitempty"`
	AuthExpireAtMs    int64  `json:"auth_expire_at_ms,omitempty"`
	TokenPath         string `json:"token_path"`
}

func newWhoamiCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "显示当前登录身份与凭证有效期（纯本地读取，不发网络请求）",
		Long: `读取 room login 保存的本地用户凭证，显示身份与各级有效期。

纯本地检查：token 是否仍被服务端认可，以实际调用为准。
退出码：已登录 0；未登录 / 刷新凭证过期 / 授权到一年硬顶 exit 3（error.type=auth）。`,
		Example: `  room whoami
  room whoami --json`,
		Args: validationArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			token := a.userTokens().Read()
			if token == nil {
				return output.Errf(output.TypeAuth, "运行 room login 完成用户授权", "未登录")
			}
			// access_token 过期不算未登录（使用时自动刷新）；
			// refresh 过期或授权硬顶已过则整个授权不可用，只能重新 login
			nowMs := a.now().UnixMilli()
			if token.AuthExpireAt > 0 && nowMs >= token.AuthExpireAt {
				return output.Errf(output.TypeAuth,
					"重新运行 room login（授权自首次登录起一年到期，刷新不延长）", "登录授权已到一年硬顶")
			}
			if token.RefreshExpireAt > 0 && nowMs >= token.RefreshExpireAt {
				return output.Errf(output.TypeAuth, "重新运行 room login", "刷新凭证已过期")
			}
			if a.jsonOut {
				return output.WriteSuccess(cmd.OutOrStdout(), whoamiData{
					LoggedIn: true, AppID: env("FEISHU_APP_ID"),
					Name: token.Name, OpenID: token.OpenID, UserID: token.UserID, Scope: token.Scope,
					AccessExpireAtMs: token.AccessExpireAt, RefreshExpireAtMs: token.RefreshExpireAt,
					AuthExpireAtMs: token.AuthExpireAt, TokenPath: userTokenPath(),
				}, nil)
			}
			fmt.Fprint(cmd.OutOrStdout(), formatWhoami(token, env("FEISHU_APP_ID"), userTokenPath(), a.now(), a.loc))
			return nil
		},
	}
}

// formatWhoami 人类可读身份表格（风格对齐 sentry 自检）。
func formatWhoami(token *feishu.StoredUserToken, appID, path string, now time.Time, loc *time.Location) string {
	var b strings.Builder
	b.WriteString("当前登录身份\n\n")
	w := tabwriter.NewWriter(&b, 2, 4, 2, ' ', 0)
	if token.Name != "" {
		fmt.Fprintf(w, "姓名:\t%s\n", token.Name)
	}
	if token.OpenID != "" {
		fmt.Fprintf(w, "OpenID:\t%s\n", token.OpenID)
	}
	if token.UserID != "" {
		fmt.Fprintf(w, "UserID:\t%s\n", token.UserID)
	}
	if appID != "" {
		fmt.Fprintf(w, "应用:\t%s\n", appID)
	}
	if token.Scope != "" {
		fmt.Fprintf(w, "Scope:\t%s\n", token.Scope)
	}
	fmt.Fprintf(w, "访问凭证过期:\t%s\n", fmtExpiry(token.AccessExpireAt, now, loc))
	fmt.Fprintf(w, "刷新凭证过期:\t%s\n", fmtExpiry(token.RefreshExpireAt, now, loc))
	if token.AuthExpireAt > 0 {
		fmt.Fprintf(w, "授权硬顶:\t%s（到期需重新 room login）\n", fmtExpiry(token.AuthExpireAt, now, loc))
	}
	fmt.Fprintf(w, "凭证文件:\t%s\n", path)
	w.Flush()
	return b.String()
}

// fmtExpiry epoch ms → 本地时间附剩余时长；0（旧版凭证缺字段）显示未知。
func fmtExpiry(ms int64, now time.Time, loc *time.Location) string {
	if ms == 0 {
		return "未知（旧版凭证）"
	}
	t := time.UnixMilli(ms).In(loc)
	remain := t.Sub(now)
	switch {
	case remain <= 0:
		return t.Format("2006-01-02 15:04") + "（已过期）"
	case remain < 48*time.Hour:
		return fmt.Sprintf("%s（剩 %d 小时）", t.Format("2006-01-02 15:04"), int(remain.Hours()))
	default:
		return fmt.Sprintf("%s（剩 %d 天）", t.Format("2006-01-02 15:04"), int(remain.Hours()/24))
	}
}

// logoutData logout --json 的 data 载荷。
type logoutData struct {
	Status        string `json:"status"` // logged_out / not_logged_in（幂等）
	RemoteRevoked bool   `json:"remote_revoked"`
	TokenPath     string `json:"token_path"`
}

func newLogoutCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "退出登录：撤销并删除本地保存的用户凭证（幂等）",
		Long: `撤销远端授权（best-effort，需应用凭证在场）并删除本地用户凭证文件。

未登录时幂等成功（--json 时 status=not_logged_in），exit 0。之后需重新 room login。`,
		Example: `  room logout
  room logout --json`,
		Args: validationArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			store := a.userTokens()
			path := userTokenPath()
			if store.Read() == nil {
				if a.jsonOut {
					return output.WriteSuccess(w, logoutData{Status: "not_logged_in", TokenPath: path}, nil)
				}
				fmt.Fprintln(w, "当前未登录，无需退出")
				return nil
			}
			revoked := false
			if revoker := a.loginRevoker(); revoker != nil {
				revoked = feishu.RevokeStoredTokensBestEffort(cmd.Context(), store, revoker, a.log)
			} else {
				a.log.Warn("缺应用凭证，跳过远端撤销，仅删除本地凭证")
			}
			if err := store.Delete(); err != nil {
				return fmt.Errorf("删除本地凭证失败: %w", err)
			}
			if a.jsonOut {
				return output.WriteSuccess(w, logoutData{Status: "logged_out", RemoteRevoked: revoked, TokenPath: path}, nil)
			}
			fmt.Fprintln(w, "已退出登录（本地凭证已删除），重新使用请运行 room login")
			return nil
		},
	}
}

// loginRevoker logout 的远端撤销客户端：测试缝隙优先；缺应用凭证返回 nil（跳过远端撤销）。
func (a *app) loginRevoker() feishu.TokenRevoker {
	if a.tokenRevoker != nil {
		return a.tokenRevoker
	}
	if client := envOAuthClient(); client != nil {
		return client
	}
	return nil
}
