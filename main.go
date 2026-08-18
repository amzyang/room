package main

import (
	"fmt"
	"os"
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

	root, a := newRootCmd(resolved)
	err := root.Execute()
	if err != nil {
		// flag 解析失败时 --json 可能未生效（pflag 在首个坏 flag 处停止），
		// 兜底重扫 os.Args 保证机读错误信封契约
		output.WriteError(a.streams.Err, err, a.jsonOut || jsonRequested(os.Args[1:]))
		if output.Reportable(err) {
			sentry.CaptureException(err)
		}
	}
	sentry.Flush(2 * time.Second)
	os.Exit(output.ExitCode(err))
}
