package config

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/amzyang/room/envutil"
)

// DefaultPath 全局配置文件路径。解析顺序:
// ROOM_CONFIG 环境变量(显式文件路径,测试/Docker 注入用)>
// $XDG_CONFIG_HOME/room/config.toml > ~/.config/room/config.toml。
// 刻意不用 os.UserConfigDir:macOS 上它指向 ~/Library/Application Support,CLI 惯例是 ~/.config;
// 仅 Windows 回退到 %APPDATA%。
// 注意:在 main 中于加载 .env 之前求值,故 ROOM_CONFIG 只认 shell 层。
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return defaultPath(os.Getenv, home, runtime.GOOS)
}

func defaultPath(getenv func(string) string, home, goos string) string {
	if p := envutil.CleanEnvValue(getenv("ROOM_CONFIG")); p != "" {
		return p
	}
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "room", "config.toml")
	}
	if goos == "windows" {
		if ad := getenv("APPDATA"); ad != "" {
			return filepath.Join(ad, "room", "config.toml")
		}
	}
	return filepath.Join(home, ".config", "room", "config.toml")
}
