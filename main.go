package main

import (
	"fmt"
	"os"
	"time"
	_ "time/tzdata"

	sentry "github.com/getsentry/sentry-go"
	"github.com/joho/godotenv"

	"github.com/amzyang/room/envutil"
)

func main() {
	_ = godotenv.Load()

	dsn := envutil.CleanEnvValue(os.Getenv("SENTRY_DSN"))
	if dsn != "" {
		if err := sentry.Init(sentry.ClientOptions{Dsn: dsn}); err == nil {
			defer sentry.Flush(2 * time.Second)
		}
	}

	if err := newRootCmd().Execute(); err != nil {
		if dsn != "" {
			sentry.CaptureException(err)
		}
		fmt.Fprintln(os.Stderr, err)
		sentry.Flush(2 * time.Second)
		os.Exit(1)
	}
}
