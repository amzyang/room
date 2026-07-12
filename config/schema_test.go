package config

import (
	"strings"
	"testing"
)

func TestRegistryKeysUnique(t *testing.T) {
	envSeen := map[string]bool{}
	tomlSeen := map[string]bool{}
	for _, it := range Registry {
		if envSeen[it.EnvKey] {
			t.Errorf("EnvKey 重复: %s", it.EnvKey)
		}
		envSeen[it.EnvKey] = true
		if tomlSeen[it.TOMLKey()] {
			t.Errorf("TOML key 重复: %s", it.TOMLKey())
		}
		tomlSeen[it.TOMLKey()] = true
	}
}

func TestRegistryDefaultsValid(t *testing.T) {
	for _, it := range Registry {
		if it.Default == "" {
			continue
		}
		if _, err := it.Normalize(it.Default); err != nil {
			t.Errorf("%s 默认值 %q 未通过自身校验: %v", it.TOMLKey(), it.Default, err)
		}
	}
}

func TestLookup(t *testing.T) {
	tests := []struct {
		arg     string
		wantEnv string
		wantErr bool
	}{
		{"FEISHU_APP_ID", "FEISHU_APP_ID", false},
		{"feishu_app_id", "FEISHU_APP_ID", false}, // env 名大小写不敏感
		{"feishu.app_id", "FEISHU_APP_ID", false},
		{"FEISHU.APP_ID", "FEISHU_APP_ID", false}, // TOML key 大小写不敏感
		{"MODEL", "MODEL", false},
		{"model", "MODEL", false},
		{"nlp.model", "MODEL", false},
		{"bogus_key_xyz", "", true},
		{"feishu.bogus", "", true},
		{"network.tls_insecure", "", true}, // 已移除:始终启用 TLS 证书校验
		{"ROOM_TLS_INSECURE", "", true},
	}
	for _, tt := range tests {
		it, err := Lookup(tt.arg)
		if tt.wantErr {
			if err == nil {
				t.Errorf("Lookup(%q) 应报错", tt.arg)
			}
			continue
		}
		if err != nil {
			t.Errorf("Lookup(%q) 意外报错: %v", tt.arg, err)
			continue
		}
		if it.EnvKey != tt.wantEnv {
			t.Errorf("Lookup(%q) = %s, want %s", tt.arg, it.EnvKey, tt.wantEnv)
		}
	}
}

func TestLookupSuggestion(t *testing.T) {
	_, err := Lookup("app_id")
	if err == nil {
		t.Fatal("Lookup(app_id) 应报错(无该裸 env 名)")
	}
	if !strings.Contains(err.Error(), "feishu.app_id") {
		t.Errorf("错误信息应含近似建议 feishu.app_id: %v", err)
	}
}

func TestNormalize(t *testing.T) {
	get := func(env string) Item {
		it, ok := ByEnvKey(env)
		if !ok {
			t.Fatalf("schema 缺少 %s", env)
		}
		return it
	}
	tests := []struct {
		env     string
		in      string
		want    string
		wantErr bool
	}{
		{"ROOM_SIZE", "15", "15", false},
		{"ROOM_SIZE", "abc", "", true},
		{"ROOM_SIZE", "", "", true},                 // int 不允许显式空,请用 unset
		{"FEISHU_AUTH_MODE", "USER", "user", false}, // enum 大小写不敏感,归一化为小写
		{"FEISHU_AUTH_MODE", "bogus", "", true},
		{"FEISHU_AUTH_MODE", "", "", true},
		{"ROOM_LIST", " 会议室A , 会议室B ", "会议室A,会议室B", false},
		{"ROOM_LIST", ",,", "", true},
		{"ROOM_LIST", "", "", false},  // 空列表允许
		{"SENTRY_DSN", "", "", false}, // string 显式空(禁用语义)
		{"FEISHU_APP_ID", "  cli_x  ", "cli_x", false},
	}
	for _, tt := range tests {
		got, err := get(tt.env).Normalize(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("%s.Normalize(%q) 应报错", tt.env, tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s.Normalize(%q) 意外报错: %v", tt.env, tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%s.Normalize(%q) = %q, want %q", tt.env, tt.in, got, tt.want)
		}
	}
}
