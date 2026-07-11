package feishu

import (
	"context"
	"log/slog"
)

// TokenRevoker token 撤销能力的 consumer 侧接口，OAuthClient 天然满足。
type TokenRevoker interface {
	RevokeToken(ctx context.Context, token, tokenTypeHint string) error
}

// RevokeStoredTokensBestEffort 对 store 中已保存的用户凭证做 best-effort 远端撤销，不修改 store。
// 优先撤 refresh_token（撤销后整个授权失效），失败再退回 access_token；
// 失败只写 warn 日志、不返回错误。store 无凭证时为 no-op。
func RevokeStoredTokensBestEffort(ctx context.Context, store UserTokenStore, revoker TokenRevoker, log *slog.Logger) {
	token := store.Read()
	if token == nil {
		return
	}
	if err := revoker.RevokeToken(ctx, token.RefreshToken, "refresh_token"); err != nil {
		log.Warn("撤销 refresh_token 失败", "error", err)
		if err := revoker.RevokeToken(ctx, token.AccessToken, "access_token"); err != nil {
			log.Warn("撤销 access_token 失败", "error", err)
		}
	}
}
