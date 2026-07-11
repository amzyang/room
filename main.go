package main

import (
	"fmt"
	"os"
	"time"
	_ "time/tzdata"

	sentry "github.com/getsentry/sentry-go"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	err := newRootCmd().Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		sentry.CaptureException(err)
	}
	sentry.Flush(2 * time.Second)
	if err != nil {
		os.Exit(1)
	}
}
