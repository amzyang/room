package main

import (
	"log/slog"
	"time"

	"github.com/spf13/cobra"

	"github.com/amzyang/room/config"
	"github.com/amzyang/room/logx"
)

// app 持有一次命令运行所需的共享状态，由 root 命令的 PersistentPreRun 装配。
type app struct {
	run   bool
	debug bool
	log   *slog.Logger
	loc   *time.Location
	now   func() time.Time
	cfg   *config.Resolved // Bootstrap 产物：生效配置与来源，config 命令族用
}

func newRootCmd(cfg *config.Resolved) *cobra.Command {
	a := &app{cfg: cfg}

	root := &cobra.Command{
		Use:           "room",
		Short:         "会议室预订自动化工具",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			a.loc, _ = time.LoadLocation("Asia/Shanghai")
			a.log = logx.New(a.debug, a.loc)
			a.now = time.Now
			initSentry(cmd)
		},
	}

	root.PersistentFlags().BoolVar(&a.run, "run", false, "实际执行预订操作（没有此参数则为演练模式）")
	root.PersistentFlags().BoolVar(&a.debug, "debug", false, "启用调试模式，输出所有HTTP请求/响应详情")
	root.PersistentFlags().String("sentry-dsn", "", "Sentry DSN（覆盖 SENTRY_DSN 环境变量；显式设空则禁用上报）")

	root.AddCommand(
		newAutoCmd(a),
		newBookCmd(a),
		newListCmd(a),
		newCancelCmd(a),
		newConfigCmd(a),
		newInitCmd(a),
		newLoginCmd(a),
		newNotifyCmd(a),
	)
	return root
}
