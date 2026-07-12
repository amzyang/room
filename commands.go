package main

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/amzyang/room/config"
	"github.com/amzyang/room/logx"
	"github.com/amzyang/room/output"
)

// app 持有一次命令运行所需的共享状态，由 root 命令的 PersistentPreRun 装配。
type app struct {
	dryRun  bool // 全局 --dryrun：演练模式，仅 auto 支持（其余命令 fail-fast 拒绝）
	debug   bool
	jsonOut bool // 全局 --json：输出机读信封并禁用交互
	log     *slog.Logger
	loc     *time.Location
	now     func() time.Time
	cfg     *config.Resolved // Bootstrap 产物：生效配置与来源，config 命令族用
	// streams 只承担进程级职责：TTY 检测、交互输入（prompter）、main 的错误信封。
	// 命令输出（信封与人类可读结果）一律走 cobra 的 cmd.OutOrStdout()。
	streams *output.Streams

	// newService book/list/cancel 的服务构造缝隙，测试替换为 fake。
	newService func(ctx context.Context, dryRun bool) (bookingService, error)
}

func newRootCmd(cfg *config.Resolved) (*cobra.Command, *app) {
	a := &app{cfg: cfg, streams: output.DetectStreams()}
	a.newService = func(ctx context.Context, dryRun bool) (bookingService, error) {
		svc, err := a.newBookingService(ctx, dryRun)
		if err != nil {
			return nil, err
		}
		return svc, nil
	}

	root := &cobra.Command{
		Use:   "room",
		Short: "会议室预订自动化工具",
		Long: `room 基于飞书开放平台预订会议室，同时面向人类交互与 agent/脚本消费。

Agent/脚本快速上手（全程非交互）：
  room config list --json                        查看配置齐备情况
  room init --no-wait --json                     两段式创建应用（返回 device_code 与恢复命令）
  room init --device-code <code> --json          用户授权后恢复，写入应用凭证
  room login --no-wait --json                    两段式用户授权（同上）
  room book -d 07-15 -t 14:00-15:00 --title 周会 --json
  room list --json                               列出日程（events[].event_id 供 cancel 用）
  room cancel --event-id <id> --yes --json       取消事件（幂等）

输出契约（--json）：
  stdout 每行一个成功信封 {"ok":true,"data":...,"meta":...}
  stderr 日志 + 错误信封 {"ok":false,"error":{"type","message","hint",...}}（最后一行）
  --json 隐含非交互：必填项缺失立即报错（exit 2），绝不挂起等待输入

退出码：
  0  成功（book 的 exit 0 ⟺ 房间订上了）
  1  API/业务失败（book 未订到细分 error.type: no_room/conflict/holiday_skipped）
  2  参数校验失败
  3  认证/配置缺失
  10 需显式确认（加 --yes / --force）`,
		Example: `  room book "明天下午3点 开发周会"
  room book -d 07-15 -t 14:00-15:00 --title 架构评审 --json
  room list -d 7 --json
  room cancel --event-id <event_id> --yes --json`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		// 显式 Args + RunE 接管未知子命令：默认 cobra 在 flag 解析前就报
		// "unknown command"（--json 不生效、退出码 1），破坏机读契约。
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			hint := "room --help 查看可用命令"
			if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
				hint = "是否想用 room " + strings.Join(s, " / room ")
			}
			return output.Errf(output.TypeValidation, hint, "未知命令: %q", args[0])
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			a.loc, _ = time.LoadLocation("Asia/Shanghai")
			a.log = logx.NewAt(a.logLevel(), a.loc)
			a.now = time.Now
			initSentry(cmd)
			// --dryrun 只有 auto 实现了演练语义；其余命令静默忽略等于
			// 「用户以为在演练、实际真实执行」，必须 fail-fast
			if a.dryRun && cmd.Name() != "auto" {
				return output.Errf(output.TypeValidation,
					"演练仅 auto 支持：room auto --dryrun；其余命令总是真实执行",
					"%s 不支持 --dryrun", cmd.CommandPath())
			}
			return nil
		},
	}

	// 接管未知子命令后 cobra 不再代设建议距离，需显式开启 Did-you-mean
	root.SuggestionsMinimumDistance = 2

	root.PersistentFlags().BoolVar(&a.dryRun, "dryrun", false, "演练模式：只输出预订计划不真实执行（仅 auto 支持）")
	root.PersistentFlags().BoolVar(&a.debug, "debug", false, "启用调试模式，输出所有HTTP请求/响应详情")
	root.PersistentFlags().BoolVar(&a.jsonOut, "json", false, "输出机读 JSON 信封（stdout: {ok,data}，stderr: {ok,error}）并禁用交互")
	root.PersistentFlags().String("sentry-dsn", "", "Sentry DSN（覆盖 SENTRY_DSN 环境变量；显式设空则禁用上报）")
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return output.Errf(output.TypeValidation, cmd.CommandPath()+" --help 查看用法", "%s", err)
	})

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
	return root, a
}

// logLevel --json 模式下提级到 Warn 减少 stderr 噪音；--debug 显式要求时仍最详细。
func (a *app) logLevel() slog.Level {
	switch {
	case a.debug:
		return slog.LevelDebug
	case a.jsonOut:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// validationArgs 把 cobra 位置参数校验错误归入 validation（exit 2）。
func validationArgs(fn cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := fn(cmd, args); err != nil {
			return output.Errf(output.TypeValidation, cmd.CommandPath()+" --help 查看用法", "%s", err)
		}
		return nil
	}
}
