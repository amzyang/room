package feishu

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// StoredUserToken 本地持久化的用户身份凭证。
// 过期时间为绝对毫秒时间戳（epoch ms），与 Node 版 feishu-user-token.json 格式互换。
type StoredUserToken struct {
	AccessToken     string `json:"accessToken"`
	AccessExpireAt  int64  `json:"accessExpireAt"`
	RefreshToken    string `json:"refreshToken"`
	RefreshExpireAt int64  `json:"refreshExpireAt"`
	// 授权硬顶 auth_exp（epoch ms）：首次授权 + 1 年，刷新不延长；旧凭证可能无此字段
	AuthExpireAt int64  `json:"authExpireAt,omitempty"`
	OpenID       string `json:"openId,omitempty"`
	UserID       string `json:"userId,omitempty"`
	Name         string `json:"name,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// UserTokenStore 凭证存储抽象，便于测试注入内存实现。
type UserTokenStore interface {
	Read() *StoredUserToken
	Write(token *StoredUserToken) error
}

const (
	accessTokenSkew  = 5 * time.Minute
	refreshTokenSkew = time.Minute
	authExpiryWarn   = 14 * 24 * time.Hour
)

// IsAccessTokenValid access_token 提前 5 分钟视为过期，避免临界时刻请求失败。
func IsAccessTokenValid(token *StoredUserToken, now time.Time) bool {
	return token.AccessToken != "" && token.AccessExpireAt > now.Add(accessTokenSkew).UnixMilli()
}

// IsRefreshTokenValid refresh_token 提前 1 分钟视为过期。
func IsRefreshTokenValid(token *StoredUserToken, now time.Time) bool {
	return token.RefreshToken != "" && token.RefreshExpireAt > now.Add(refreshTokenSkew).UnixMilli()
}

// IsAuthNearExpiry 授权硬顶剩余不足 14 天；无 authExpireAt（旧凭证）时返回 false 不打扰。
func IsAuthNearExpiry(token *StoredUserToken, now time.Time) bool {
	if token.AuthExpireAt == 0 {
		return false
	}
	return token.AuthExpireAt-now.UnixMilli() < authExpiryWarn.Milliseconds()
}

// FileUserTokenStore 基于本地 JSON 文件的凭证存储，文件权限 0600。
type FileUserTokenStore struct {
	Path string
}

func (s *FileUserTokenStore) Read() *StoredUserToken {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil
	}
	var token StoredUserToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil
	}
	if token.AccessToken == "" || token.RefreshToken == "" {
		return nil
	}
	return &token
}

// Delete 删除本地凭证文件；文件不存在视为成功。
func (s *FileUserTokenStore) Delete() error {
	if err := os.Remove(s.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *FileUserTokenStore) Write(token *StoredUserToken) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path, data, 0o600)
}
