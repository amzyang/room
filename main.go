package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
	_ "time/tzdata"

	sentry "github.com/getsentry/sentry-go"

	"github.com/amzyang/room/config"
	"github.com/amzyang/room/output"
)

func main() {
	resolved := config.Bootstrap(config.DefaultPath())
	if resolved.Warning != "" {
		fmt.Fprintln(os.Stderr, "警告: "+resolved.Warning)
	}
	migrateLegacyCache(".cache", config.CacheDir())

	root, a := newRootCmd(resolved)
	err := root.Execute()
	if err != nil {
		output.WriteError(os.Stderr, err, a.jsonOut)
		if output.Reportable(err) {
			sentry.CaptureException(err)
		}
	}
	sentry.Flush(2 * time.Second)
	os.Exit(output.ExitCode(err))
}

// migrateLegacyCache 旧版缓存落在运行目录 ./.cache,一次性 best-effort 整体搬到
// 配置目录旁的 cache/(几个版本后可删)。任一失败视作无旧缓存:重新 login/重建即可。
func migrateLegacyCache(oldDir, newDir string) {
	if _, err := os.Stat(newDir); err == nil {
		return
	}
	if _, err := os.Stat(oldDir); err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(newDir), 0o700)
	_ = os.Rename(oldDir, newDir)
}
