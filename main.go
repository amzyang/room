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
		output.WriteError(a.streams.Err, err, a.jsonOut)
		if output.Reportable(err) {
			sentry.CaptureException(err)
		}
	}
	sentry.Flush(2 * time.Second)
	os.Exit(output.ExitCode(err))
}
