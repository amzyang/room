package feishu

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func fakeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func TestParseAuthExpireAt(t *testing.T) {
	if got := ParseAuthExpireAt(fakeJWT(t, map[string]any{"auth_exp": 1814494697})); got != 1814494697000 {
		t.Errorf("auth_exp = %d, want 1814494697000", got)
	}
	if got := ParseAuthExpireAt(fakeJWT(t, map[string]any{"exp": 123})); got != 0 {
		t.Errorf("missing auth_exp = %d, want 0", got)
	}
	if got := ParseAuthExpireAt("not-a-jwt"); got != 0 {
		t.Errorf("non-JWT = %d, want 0", got)
	}
	if got := ParseAuthExpireAt("header.@@@.sig"); got != 0 {
		t.Errorf("invalid payload = %d, want 0", got)
	}
}
