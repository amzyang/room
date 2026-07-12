package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultPathResolution(t *testing.T) {
	getenv := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	tests := []struct {
		name string
		env  map[string]string
		home string
		goos string
		want string
	}{
		{
			name: "ROOM_CONFIG 最优先",
			env:  map[string]string{"ROOM_CONFIG": "/tmp/x.toml", "XDG_CONFIG_HOME": "/xdg"},
			home: "/home/u", goos: "darwin",
			want: "/tmp/x.toml",
		},
		{
			name: "XDG_CONFIG_HOME 次之",
			env:  map[string]string{"XDG_CONFIG_HOME": "/xdg"},
			home: "/home/u", goos: "darwin",
			want: filepath.Join("/xdg", "room", "config.toml"),
		},
		{
			name: "默认 ~/.config(darwin 不用 Application Support)",
			env:  map[string]string{},
			home: "/home/u", goos: "darwin",
			want: filepath.Join("/home/u", ".config", "room", "config.toml"),
		},
		{
			name: "windows 回退 APPDATA",
			env:  map[string]string{"APPDATA": filepath.Join("C:", "Users", "u", "Roaming")},
			home: "", goos: "windows",
			want: filepath.Join("C:", "Users", "u", "Roaming", "room", "config.toml"),
		},
	}
	for _, tt := range tests {
		if got := defaultPath(getenv(tt.env), tt.home, tt.goos); got != tt.want {
			t.Errorf("%s: defaultPath = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// 缓存目录 = 配置文件所在目录 + cache,随 ROOM_CONFIG / XDG_CONFIG_HOME 走。
func TestCacheDir(t *testing.T) {
	getenv := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "跟随 ROOM_CONFIG 所在目录",
			env:  map[string]string{"ROOM_CONFIG": "/tmp/room-x/config.toml"},
			want: filepath.Join("/tmp/room-x", "cache"),
		},
		{
			name: "跟随 XDG_CONFIG_HOME",
			env:  map[string]string{"XDG_CONFIG_HOME": "/xdg"},
			want: filepath.Join("/xdg", "room", "cache"),
		},
		{
			name: "默认 ~/.config/room/cache",
			env:  map[string]string{},
			want: filepath.Join("/home/u", ".config", "room", "cache"),
		},
	}
	for _, tt := range tests {
		if got := cacheDir(getenv(tt.env), "/home/u", "darwin"); got != tt.want {
			t.Errorf("%s: cacheDir = %q, want %q", tt.name, got, tt.want)
		}
	}
}
