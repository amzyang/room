package main

import (
	"fmt"
	"os"
	"time"
	_ "time/tzdata"

	sentry "github.com/getsentry/sentry-go"

	"github.com/amzyang/room/config"
)

func main() {
	resolved := config.Bootstrap(".env", config.DefaultPath())
	if resolved.Warning != "" {
		fmt.Fprintln(os.Stderr, "警告: "+resolved.Warning)
	}

	err := newRootCmd(resolved).Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		sentry.CaptureException(err)
	}
	sentry.Flush(2 * time.Second)
	if err != nil {
		os.Exit(1)
	}
}
