package feishu

import (
	"context"
	"net/http"
)

const authUserInfoURL = "https://open.feishu.cn/open-apis/authen/v1/user_info"

// UserIdentity 当前授权用户身份（login 时获取并随凭证持久化）。
// UserID 依赖应用具备 contact:user.employee_id:readonly 权限，可能为空。
type UserIdentity struct {
	OpenID string
	UserID string
	Name   string
}

// GetUserInfo 用 user_access_token 获取当前授权用户身份。
// GET /open-apis/authen/v1/user_info，Bearer 鉴权。
func (c *OAuthClient) GetUserInfo(ctx context.Context, accessToken string) (*UserIdentity, error) {
	endpoint := c.userInfoURL
	if endpoint == "" {
		endpoint = authUserInfoURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			OpenID string `json:"open_id"`
			UserID string `json:"user_id"`
			Name   string `json:"name"`
		} `json:"data"`
	}
	if err := c.doJSON(req, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, &APIError{Code: resp.Code, Msg: resp.Msg}
	}
	return &UserIdentity{OpenID: resp.Data.OpenID, UserID: resp.Data.UserID, Name: resp.Data.Name}, nil
}
