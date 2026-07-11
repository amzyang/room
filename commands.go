package main

import (
	"log/slog"
	"time"

	"github.com/spf13/cobra"

	"github.com/amzyang/room/logx"
)

// app 持有一次命令运行所需的共享状态，由 root 命令的 PersistentPreRun 装配。
type app struct {
	run   bool
	debug bool
	log   *slog.Logger
	loc   *time.Location
	now   func() time.Time
}

func newRootCmd() *cobra.Command {
	a := &app{}

	root := &cobra.Command{
		Use:           "room",
		Short:         "会议室预订自动化工具",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(_ *cobra.Command, _ []string) {
			a.loc, _ = time.LoadLocation("Asia/Shanghai")
			a.log = logx.New(a.debug, a.loc)
			a.now = time.Now
		},
	}

	root.PersistentFlags().BoolVar(&a.run, "run", false, "实际执行预订操作（没有此参数则为演练模式）")
	root.PersistentFlags().BoolVar(&a.debug, "debug", false, "启用调试模式，输出所有HTTP请求/响应详情")

	root.AddCommand(
		newAutoCmd(a),
		newBookCmd(a),
		newListCmd(a),
		newCancelCmd(a),
		newLoginCmd(a),
		newNotifyCmd(a),
	)
	return root
}
