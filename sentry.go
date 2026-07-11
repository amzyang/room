package main

import (
	"os"

	sentry "github.com/getsentry/sentry-go"
	"github.com/spf13/cobra"

	"github.com/amzyang/room/envutil"
)

// builtinSentryDSN 由 goreleaser 经 ldflags 注入，作为运行时未指定 DSN 时的 fallback。
var builtinSentryDSN string

// resolveSentryDSN 优先级：flag > SENTRY_DSN 环境变量 > 编译内置。
// flag 或 env 显式设置但清洗后为空，视为禁用（返回空串，不再 fallback）。
func resolveSentryDSN(flagValue string, flagSet bool, envValue string, envSet bool, builtin string) string {
	if flagSet {
		return envutil.CleanEnvValue(flagValue)
	}
	if envSet {
		return envutil.CleanEnvValue(envValue)
	}
	return envutil.CleanEnvValue(builtin)
}

// initSentry 在 cobra 解析完 flag 后调用（PersistentPreRun）。
func initSentry(cmd *cobra.Command) {
	flagValue, _ := cmd.Flags().GetString("sentry-dsn")
	envValue, envSet := os.LookupEnv("SENTRY_DSN")
	dsn := resolveSentryDSN(flagValue, cmd.Flags().Changed("sentry-dsn"), envValue, envSet, builtinSentryDSN)
	if dsn == "" {
		return
	}
	_ = sentry.Init(sentry.ClientOptions{Dsn: dsn, Release: version})
}
