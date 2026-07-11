package feishu

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTokenValidity(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	token := func(accessAt, refreshAt int64) *StoredUserToken {
		return &StoredUserToken{
			AccessToken:     "a",
			AccessExpireAt:  accessAt,
			RefreshToken:    "r",
			RefreshExpireAt: refreshAt,
		}
	}

	// access：5 分钟 skew 边界
	fiveMin := int64(5 * 60 * 1000)
	if IsAccessTokenValid(token(now.UnixMilli()+fiveMin, 0), now) {
		t.Error("access token exactly at skew boundary should be invalid")
	}
	if !IsAccessTokenValid(token(now.UnixMilli()+fiveMin+1, 0), now) {
		t.Error("access token beyond skew should be valid")
	}
	if IsAccessTokenValid(&StoredUserToken{AccessExpireAt: now.UnixMilli() + 2*fiveMin}, now) {
		t.Error("empty accessToken should be invalid")
	}

	// refresh：1 分钟 skew 边界
	oneMin := int64(60 * 1000)
	if IsRefreshTokenValid(token(0, now.UnixMilli()+oneMin), now) {
		t.Error("refresh token exactly at skew boundary should be invalid")
	}
	if !IsRefreshTokenValid(token(0, now.UnixMilli()+oneMin+1), now) {
		t.Error("refresh token beyond skew should be valid")
	}
}

func TestIsAuthNearExpiry(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	fourteenDays := int64(14 * 24 * 60 * 60 * 1000)

	if IsAuthNearExpiry(&StoredUserToken{}, now) {
		t.Error("missing authExpireAt should not warn")
	}
	if IsAuthNearExpiry(&StoredUserToken{AuthExpireAt: now.UnixMilli() + fourteenDays + 1}, now) {
		t.Error("far-future authExpireAt should not warn")
	}
	if !IsAuthNearExpiry(&StoredUserToken{AuthExpireAt: now.UnixMilli() + fourteenDays - 1}, now) {
		t.Error("near authExpireAt should warn")
	}
	if !IsAuthNearExpiry(&StoredUserToken{AuthExpireAt: now.UnixMilli() - 1}, now) {
		t.Error("expired authExpireAt should warn")
	}
}

// Node 版写出的凭证文件应能被 Go 版读出，且 Go 版写出的字段名与 Node 版一致。
func TestFileUserTokenStoreNodeCompat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "feishu-user-token.json")

	nodeWritten := `{
  "accessToken": "at-1",
  "accessExpireAt": 1700000000000,
  "refreshToken": "rt-1",
  "refreshExpireAt": 1702000000000,
  "authExpireAt": 1730000000000,
  "scope": "calendar:calendar offline_access"
}`
	if err := os.WriteFile(path, []byte(nodeWritten), 0o600); err != nil {
		t.Fatal(err)
	}

	store := &FileUserTokenStore{Path: path}
	token := store.Read()
	if token == nil {
		t.Fatal("failed to read node-written token")
	}
	if token.AccessToken != "at-1" || token.AccessExpireAt != 1700000000000 ||
		token.RefreshToken != "rt-1" || token.AuthExpireAt != 1730000000000 {
		t.Errorf("unexpected token: %+v", token)
	}

	if err := store.Write(token); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600", info.Mode().Perm())
	}

	data, _ := os.ReadFile(path)
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"accessToken", "accessExpireAt", "refreshToken", "refreshExpireAt", "authExpireAt", "scope"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("written JSON missing key %q (Node compat)", key)
		}
	}

	// 缺 accessToken/refreshToken 的文件视为无凭证
	if err := os.WriteFile(path, []byte(`{"accessToken":"only"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if store.Read() != nil {
		t.Error("token without refreshToken should read as nil")
	}
}
