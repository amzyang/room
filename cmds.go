package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/amzyang/room/booking"
	"github.com/amzyang/room/config"
	"github.com/amzyang/room/feishu"
	"github.com/amzyang/room/nlp"
	"github.com/amzyang/room/output"
)

func newAutoCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "auto",
		Short: "根据 TASK_FORMAT 自动预订会议室（--dryrun 演练）",
		Long: `按 booking.task_format 配置的任务 DSL 在可预订窗口内批量预订。

TASK_FORMAT 语法（多任务用 | 分隔）：
  dayOfWeek,startTime-endTime,frequency[:interval[:startDate]],participants,title
  例：fri,11:00:00-12:00:00,weekly,alice:bob,项目周会

默认真实批量预订；加全局 --dryrun 演练（只输出计划，不执行）。
--json 输出逐条结果 results[].status：planned（--dryrun 演练）/ booked / no_room /
conflict / holiday_skipped / no_participants / failed；批量语义整体 exit 0，
靠 status 区分单条结果。room login 的授权用户会自动加入每条任务的参会人。`,
		Example: `  room auto --dryrun   # 演练：查看将要预订哪些时段
  room auto --json`,
		Args: validationArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			service, err := a.newService(ctx, a.dryRun)
			if err != nil {
				return err
			}
			results, err := service.AutoBook(ctx, a.dryRun)
			if err != nil {
				return err
			}
			if a.jsonOut {
				if results == nil {
					results = []booking.BookResult{}
				}
				return output.WriteSuccess(cmd.OutOrStdout(), struct {
					Results []booking.BookResult `json:"results"`
				}{results}, &output.Meta{Count: len(results), DryRun: a.dryRun})
			}
			return nil
		},
	}
}

// listWindow 计算 list/cancel 的查询窗口与范围描述（纯函数）。
// date 非空查单日 [date 00:00, 次日 00:00)，与显式 --days 互斥；否则查未来 days 天。
func listWindow(now time.Time, days int, daysChanged bool, date string, loc *time.Location) (from, to time.Time, scope string, err error) {
	if date != "" {
		if daysChanged {
			return time.Time{}, time.Time{}, "", output.Errf(output.TypeValidation,
				"--date 查单日、--days 查未来窗口，二选一", "--date 与 --days 互斥")
		}
		if shortDateRegex.MatchString(date) {
			date = fmt.Sprintf("%s-%s", now.In(loc).Format("2006"), date)
		}
		normalized, err := normalizeDate(date, loc)
		if err != nil {
			return time.Time{}, time.Time{}, "", output.Errf(output.TypeValidation,
				"使用 MM-DD 或 YYYY-MM-DD 格式", "无效的日期格式: %s", date)
		}
		from, _ = time.ParseInLocation("2006-01-02", normalized, loc)
		return from, from.AddDate(0, 0, 1), normalized + " 当天", nil
	}
	if days < 1 {
		return time.Time{}, time.Time{}, "", output.Errf(output.TypeValidation,
			"--days 需为正整数", "无效的天数: %d", days)
	}
	return now, now.AddDate(0, 0, days), fmt.Sprintf("未来 %d 天内", days), nil
}

// listData list --json 的 data 载荷：--days 模式含 days，--date 模式含 date。
type listData struct {
	Days   int                    `json:"days,omitempty"`
	Date   string                 `json:"date,omitempty"`
	Mine   bool                   `json:"mine"`
	Events []booking.EventSummary `json:"events"`
}

func newListCmd(a *app) *cobra.Command {
	var days int
	var date string
	var mine bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出日历事件（--date 查单日，--mine 仅你组织的）",
		Long: `列出日历事件（默认未来 31 天，过滤已取消/删除）。
--date 查指定单日（MM-DD 补当年 / YYYY-MM-DD，与 --days 互斥）；
--mine 仅显示你组织的事件（即 room cancel 的候选集）。
--json 输出 data.events[]，其中 event_id 可直接用于 room cancel --event-id。`,
		Example: `  room list -d 7
  room list --date 07-20 --mine
  room list --json`,
		Args: validationArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			from, to, scope, err := listWindow(a.now().In(a.loc), days, cmd.Flags().Changed("days"), date, a.loc)
			if err != nil {
				return err
			}
			service, err := a.newService(ctx, false)
			if err != nil {
				return err
			}
			events, err := service.ListEvents(ctx, from, to, mine)
			if err != nil {
				return err
			}
			if a.jsonOut {
				if events == nil {
					events = []booking.EventSummary{}
				}
				data := listData{Mine: mine, Events: events}
				if date != "" {
					data.Date = from.Format("2006-01-02")
				} else {
					data.Days = days
				}
				return output.WriteSuccess(cmd.OutOrStdout(), data, &output.Meta{Count: len(events)})
			}
			fmt.Fprint(cmd.OutOrStdout(), booking.FormatEventList(events, scope, mine))
			return nil
		},
	}
	cmd.Flags().IntVarP(&days, "days", "d", defaultCancelDays, "显示未来几天的事件（与 --date 互斥）")
	cmd.Flags().StringVar(&date, "date", "", "只看指定日期（MM-DD 或 YYYY-MM-DD）")
	cmd.Flags().BoolVar(&mine, "mine", false, "仅显示你组织的事件（即 cancel 的候选集）")
	return cmd
}

func newCancelCmd(a *app) *cobra.Command {
	var days int
	var eventIDs []string
	var yes bool
	cmd := &cobra.Command{
		Use:   "cancel",
		Short: "取消日历事件（交互式选择，或 --event-id 直接指定，可重复传参批量取消）",
		Long: `两种模式：
  交互模式：room cancel 列出你组织的事件，输入编号并确认取消（仅终端环境）
  直接模式：room cancel --event-id <id> --yes（agent/脚本；ID 用 room list --json 获取）

--event-id 可重复传参批量取消：单条失败不中断，results[].status 区分
cancelled / already_cancelled / failed，整体 exit 0（对齐 auto 批量语义）。
单个 --event-id 时输出契约不变，取消失败按原语义报错。

幂等：事件已被取消/删除时同样成功返回（--json 时 status=already_cancelled），exit 0。
非交互环境：缺 --event-id 报 exit 2；有 --event-id 但缺 --yes 报 exit 10（confirmation_required）。`,
		Example: `  room cancel
  room cancel --event-id <event_id> --yes --json
  room cancel --event-id <id1> --event-id <id2> --yes --json`,
		Args: validationArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			w := cmd.OutOrStdout()

			// fail-fast 守卫在任何网络请求之前
			if len(eventIDs) == 0 && !a.interactive() {
				return output.Errf(output.TypeValidation,
					"运行 room cancel --event-id <id> --yes；事件ID用 room list --json 获取",
					"非交互环境需要 --event-id 指定要取消的事件")
			}
			if len(eventIDs) > 0 && !yes && !a.interactive() {
				return output.Errf(output.TypeConfirmationRequired, "加 --yes 确认取消",
					"取消事件 %s 需要显式确认", strings.Join(eventIDs, ", "))
			}
			var from, to time.Time
			if len(eventIDs) == 0 {
				var err error
				from, to, _, err = listWindow(a.now().In(a.loc), days, false, "", a.loc)
				if err != nil {
					return err
				}
			}

			service, err := a.newService(ctx, false)
			if err != nil {
				return err
			}

			// 直接指定模式：agent/脚本用，无需列表选择
			if len(eventIDs) == 1 {
				return a.cancelByID(ctx, w, service, eventIDs[0], yes)
			}
			if len(eventIDs) > 1 {
				return a.cancelBatch(ctx, w, service, eventIDs, yes)
			}

			events, err := service.ListEvents(ctx, from, to, true)
			if err != nil {
				return err
			}

			fmt.Fprint(w, booking.FormatCancelList(events, days))
			if len(events) == 0 {
				return nil
			}

			p := a.newPrompter()
			choice := p.question(fmt.Sprintf("请输入要取消的事件编号(1 - %d)，或按回车键取消操作: ", len(events)))
			if choice == "" {
				fmt.Fprintln(w, "已取消操作")
				return nil
			}
			index, err := strconv.Atoi(choice)
			if err != nil || index < 1 || index > len(events) {
				fmt.Fprintln(w, "无效的编号")
				return nil
			}

			selected := events[index-1]
			fmt.Fprintln(w, "\n您选择取消的事件:")
			fmt.Fprintf(w, "标题: %s\n", selected.Title)
			fmt.Fprintf(w, "时间: %s - %s\n", selected.StartTime, selected.EndTime)
			location := selected.Location
			if location == "" {
				location = "无"
			}
			fmt.Fprintf(w, "地点: %s\n", location)

			confirm := strings.ToLower(p.question("\n确认取消此事件吗？(y/N): "))
			if confirm != "y" && confirm != "yes" {
				fmt.Fprintln(w, "已取消操作")
				return nil
			}

			return a.emitCancelOutcome(ctx, w, service, selected.EventID)
		},
	}
	cmd.Flags().IntVarP(&days, "days", "d", defaultCancelDays, "显示未来几天的事件")
	cmd.Flags().StringArrayVar(&eventIDs, "event-id", nil, "要取消的事件ID，可重复传参批量取消（room list --json 可获取）")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "跳过确认（--event-id 模式下非交互环境必须）")
	return cmd
}

// cancelResultItem cancel 批量模式的单条结果。
type cancelResultItem struct {
	EventID string `json:"event_id"`
	Status  string `json:"status"` // cancelled / already_cancelled / failed
	Error   string `json:"error,omitempty"`
}

// cancelBatch 批量取消：单条失败不中断，results[].status 区分结果，
// 整体 exit 0（对齐 auto 的批量语义；单个 --event-id 走 cancelByID 保持原契约）。
func (a *app) cancelBatch(ctx context.Context, w io.Writer, service bookingService, eventIDs []string, yes bool) error {
	if !yes {
		p := a.newPrompter()
		fmt.Fprintln(w, "将取消以下事件：")
		for _, id := range eventIDs {
			fmt.Fprintf(w, "  - %s\n", id)
		}
		confirm := strings.ToLower(p.question(fmt.Sprintf("\n确认取消这 %d 个事件吗？(y/N): ", len(eventIDs))))
		if confirm != "y" && confirm != "yes" {
			fmt.Fprintln(w, "已取消操作")
			return nil
		}
	}
	results := make([]cancelResultItem, 0, len(eventIDs))
	for _, id := range eventIDs {
		item := cancelResultItem{EventID: id, Status: "cancelled"}
		outcome, err := service.CancelEvent(ctx, id)
		switch {
		case err != nil:
			item.Status = "failed"
			item.Error = err.Error()
		case outcome.AlreadyCancelled:
			item.Status = "already_cancelled"
		}
		results = append(results, item)
	}
	if a.jsonOut {
		return output.WriteSuccess(w, struct {
			Results []cancelResultItem `json:"results"`
		}{results}, &output.Meta{Count: len(results)})
	}
	for _, r := range results {
		switch r.Status {
		case "failed":
			fmt.Fprintf(w, "❌ %s 取消失败: %s\n", r.EventID, r.Error)
		case "already_cancelled":
			fmt.Fprintf(w, "✅ %s 该事件已被取消\n", r.EventID)
		default:
			fmt.Fprintf(w, "✅ %s 事件已成功取消\n", r.EventID)
		}
	}
	return nil
}

// cancelByID --event-id 直接取消：无 --yes 时交互环境二次确认（非交互已在守卫处拦下）。
func (a *app) cancelByID(ctx context.Context, w io.Writer, service bookingService, eventID string, yes bool) error {
	if !yes {
		p := a.newPrompter()
		confirm := strings.ToLower(p.question(fmt.Sprintf("确认取消事件 %s 吗？(y/N): ", eventID)))
		if confirm != "y" && confirm != "yes" {
			fmt.Fprintln(w, "已取消操作")
			return nil
		}
	}
	return a.emitCancelOutcome(ctx, w, service, eventID)
}

// emitCancelOutcome 执行取消并输出结果；已删除的事件幂等成功（already_cancelled，exit 0）。
func (a *app) emitCancelOutcome(ctx context.Context, w io.Writer, service bookingService, eventID string) error {
	outcome, err := service.CancelEvent(ctx, eventID)
	if err != nil {
		return err
	}
	if a.jsonOut {
		status := "cancelled"
		if outcome.AlreadyCancelled {
			status = "already_cancelled"
		}
		return output.WriteSuccess(w, struct {
			EventID string `json:"event_id"`
			Status  string `json:"status"`
		}{eventID, status}, nil)
	}
	fmt.Fprintf(w, "✅ %s\n", cancelMessage(outcome))
	return nil
}

// cancelMessage 取消结果的人类可读文案。
func cancelMessage(outcome *booking.CancelOutcome) string {
	if outcome.AlreadyCancelled {
		return "该事件已被取消"
	}
	return "事件已成功取消"
}

const (
	// --device-code 恢复轮询时 begin 的 interval/expires_in 已丢失，取保守值（init/login 共用）
	resumePollIntervalSec  = 5
	resumePollExpiresInSec = 600
)

// validateResumeFlags 校验 --no-wait 与 --device-code 互斥（init/login 共用）。
func validateResumeFlags(noWait bool, deviceCode string) error {
	if noWait && deviceCode != "" {
		return output.Errf(output.TypeValidation, "两段式：先 --no-wait 拿 device_code，再单独 --device-code 恢复",
			"--no-wait 与 --device-code 互斥：前者发起流程，后者恢复轮询")
	}
	return nil
}

// deviceCodeRe 设备码字符白名单。resume_command 会被 agent 拼进 shell 执行，
// 飞书 API 响应或 --device-code 入参中的越界字符一律按注入拒绝。
var deviceCodeRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func deviceCodeValid(code string) bool { return deviceCodeRe.MatchString(code) }

// validateDeviceCodeFlag 校验 --device-code 入参（init/login 共用）：
// agent 脚本取值失败易传出空串或整段恢复命令，静默放行会白等一轮授权甚至构成注入。
func validateDeviceCodeFlag(changed bool, code string) error {
	if !changed {
		return nil
	}
	if code == "" {
		return output.Errf(output.TypeValidation, "传入 --no-wait 输出中的 device_code 值", "--device-code 不能为空")
	}
	if !deviceCodeValid(code) {
		return output.Errf(output.TypeValidation,
			"只传 device_code 本身（字母/数字/._-），不要包含恢复命令或引号等其他字符",
			"--device-code 含非法字符: %q", code)
	}
	return nil
}

// rejectTaintedDeviceCode 校验 API 返回的设备码（init/login 共用）：
// 越界字符意味着响应被篡改或平台变更，绝不能拼进 resume_command。
func rejectTaintedDeviceCode(code string) error {
	if deviceCodeValid(code) {
		return nil
	}
	return output.Errf(output.TypeAPI,
		"疑似响应被篡改，请检查网络环境后重试",
		"服务端返回的 device_code 含非法字符")
}

// initOptions 汇总 init 命令的 flag（机读输出走全局 --json）。
type initOptions struct {
	force      bool   // 已有应用凭证时允许覆盖（同时撤销并清除已保存的登录凭证）
	noWait     bool   // 仅发起注册并打印后立即返回、不轮询（供 agent/CI/无头环境两段式）
	deviceCode string // 用前一次 --no-wait 得到的设备码恢复轮询
	jsonOut    bool   // 由全局 --json 填充
}

// newInitCmd 构造 init 命令：匿名 device flow 自动创建飞书 PersonalAgent 个人应用
// 并把凭证写入全局 config.toml，替代去开放平台手动建应用抄 app_id/app_secret。
func newInitCmd(a *app) *cobra.Command {
	opts := initOptions{}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "一键自动创建飞书 PersonalAgent 个人应用并把凭证写入配置",
		Long: `通过匿名 OAuth 设备码流程自动创建飞书个人应用，凭证写入全局 config.toml。

三种模式：
  默认      发起注册 → 打印授权链接（尽力打开浏览器）→ 原地轮询到创建完成
  --no-wait 仅发起并打印 device_code 与恢复命令后立即返回（agent/CI 两段式第一步）
  --device-code <code>  用户授权后恢复轮询，换取并保存凭证（两段式第二步）

--json 时输出 app_registration / app_registered 两种事件信封。
已有凭证时需 --force 确认覆盖（否则 exit 10）。完成后运行 room login 授权用户身份。`,
		Example: `  room init
  room init --no-wait --json     # 拿 device_code，引导用户打开 verification_uri_complete
  room init --device-code <code> --json`,
		Args: validationArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateDeviceCodeFlag(cmd.Flags().Changed("device-code"), opts.deviceCode); err != nil {
				return err
			}
			opts.jsonOut = a.jsonOut
			return a.runInit(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&opts.force, "force", false, "覆盖已有应用凭证（同时撤销并清除已保存的登录凭证）")
	fl.BoolVar(&opts.noWait, "no-wait", false, "仅发起注册并打印 device_code 后返回，不轮询（agent/CI 两段式）")
	fl.StringVar(&opts.deviceCode, "device-code", "", "用前一次 --no-wait 得到的设备码恢复轮询")
	return cmd
}

// runInit 三种模式（与 login 的设备码流程同构）：
//   - 默认（阻塞）：发起注册 → 展示授权链接（尽力打开浏览器）→ 原地轮询到应用创建完成。
//   - --no-wait：仅发起注册并打印（含 device_code + 恢复命令）后立即返回。
//   - --device-code <code>：跳过发起，用已有设备码恢复轮询换取并保存凭证。
func (a *app) runInit(ctx context.Context, w io.Writer, opts initOptions) error {
	if err := validateResumeFlags(opts.noWait, opts.deviceCode); err != nil {
		return err
	}
	// 覆盖保护在任何网络请求之前，begin 与 --device-code 恢复两条路径都要过
	oldAppID, oldAppSecret := env("FEISHU_APP_ID"), env("FEISHU_APP_SECRET")
	if err := checkExistingAppCredentials(oldAppID, oldAppSecret, opts.force); err != nil {
		return err
	}

	tokenPath := userTokenPath()
	reg := &feishu.AppRegistrar{HTTP: feishu.NewHTTPClient(), Log: a.log, Clock: a.now}

	// 恢复轮询模式：用已有 device_code 直接轮询换取应用凭证
	if opts.deviceCode != "" {
		if !opts.jsonOut {
			fmt.Fprintln(w, "正在轮询应用创建结果...（在浏览器完成授权后将自动继续）")
		}
		creds, err := reg.Poll(ctx, opts.deviceCode, resumePollIntervalSec, resumePollExpiresInSec)
		if err != nil {
			return classifyDeviceFlowErr(err)
		}
		return a.finishInit(ctx, w, creds, oldAppID, oldAppSecret, tokenPath, opts.jsonOut)
	}

	code, err := reg.Begin(ctx)
	if err != nil {
		return output.Wrap(output.TypeAPI,
			"可改用手动方式：在 open.feishu.cn 创建应用后用 room config set feishu.app_id / feishu.app_secret 写入凭证",
			fmt.Errorf("自动创建应用失败: %w", err))
	}
	if err := rejectTaintedDeviceCode(code.DeviceCode); err != nil {
		return err
	}

	// 发起-即返回模式：打印设备码供后续 --device-code 恢复，不阻塞轮询
	if opts.noWait {
		emitAppRegistration(w, code, opts.jsonOut, true, opts.force)
		return nil
	}

	// 默认阻塞模式：展示授权入口 + TTY 下尽力打开浏览器 + 原地轮询到完成
	// （非 TTY 多为 agent/CI 宿主，弹浏览器是打扰而非帮助，与 login 同策略）
	emitAppRegistration(w, code, opts.jsonOut, false, opts.force)
	if a.streams.OutIsTerminal {
		feishu.OpenBrowser(code.VerificationURIComplete)
	}
	if !opts.jsonOut {
		fmt.Fprintln(w, "\n等待授权中...（在浏览器完成授权后将自动继续）")
	}
	creds, err := reg.Poll(ctx, code.DeviceCode, code.IntervalSec, code.ExpiresInSec)
	if err != nil {
		return classifyDeviceFlowErr(err)
	}
	return a.finishInit(ctx, w, creds, oldAppID, oldAppSecret, tokenPath, opts.jsonOut)
}

// classifyDeviceFlowErr 设备码轮询错误归类：过期可重试，拒绝授权不可。
func classifyDeviceFlowErr(err error) error {
	switch {
	case errors.Is(err, feishu.ErrRegistrationExpired):
		return output.Wrap(output.TypeAuth, "重新发起并在浏览器完成授权", err).WithRetryable()
	case errors.Is(err, feishu.ErrRegistrationDenied):
		return output.Wrap(output.TypeAuth, "重新发起并在授权页选择同意", err)
	default:
		return err
	}
}

// finishInit 注册成功后的收尾：写凭证 → 撤销旧应用登录凭证 → 提示覆盖风险。
// 先落盘再撤销：写入失败时保留旧登录态，避免「新应用已创建、旧登录又被销毁」的双输局面。
func (a *app) finishInit(ctx context.Context, w io.Writer, creds *feishu.AppCredentials, oldAppID, oldAppSecret, tokenPath string, jsonOut bool) error {
	if err := saveAppCredentials(w, a.cfg.Path, creds, jsonOut); err != nil {
		return err
	}
	a.revokeOldLoginBestEffort(ctx, oldAppID, oldAppSecret, tokenPath)
	// shell env 会压过写入的 TOML 值
	for _, key := range []string{"FEISHU_APP_ID", "FEISHU_APP_SECRET"} {
		if src := a.cfg.OverrideOf(key); src != config.SourceUnset {
			a.log.Warn(fmt.Sprintf("检测到 %s 来自 %s，将覆盖新写入的配置，请移除后再运行 room login", key, src))
		}
	}
	return nil
}

// checkExistingAppCredentials 保护已有应用凭证：覆盖需显式 --force（并意味着清除旧应用的登录凭证）。
func checkExistingAppCredentials(appID, appSecret string, force bool) error {
	if force {
		return nil
	}
	if appID != "" || appSecret != "" {
		return output.Errf(output.TypeConfirmationRequired,
			"确认替换请加 --force（将同时撤销并清除已保存的登录凭证，需重新 room login）",
			"环境变量或 config.toml 已有应用凭证（app_id %s）", maskSecret(appID))
	}
	return nil
}

// revokeOldLoginBestEffort 覆盖旧凭证前撤销旧应用的远端 token（失败仅告警），
// 并无条件删除本地用户凭证文件——旧 token 属旧应用，残留会让新应用误带失效凭证。
func (a *app) revokeOldLoginBestEffort(ctx context.Context, oldAppID, oldAppSecret, tokenPath string) {
	store := &feishu.FileUserTokenStore{Path: tokenPath}
	if oldAppID != "" && oldAppSecret != "" {
		client := &feishu.OAuthClient{HTTP: feishu.NewHTTPClient(), AppID: oldAppID, AppSecret: oldAppSecret}
		feishu.RevokeStoredTokensBestEffort(ctx, store, client, a.log)
	}
	if err := store.Delete(); err != nil {
		a.log.Warn("删除本地用户凭证失败", "path", tokenPath, "error", err)
	}
}

// emitAppRegistration 输出应用注册授权入口；showResumeHint 为 true 时附上 --device-code
// 恢复命令（--no-wait 用），withForce 为 true 时恢复命令带 --force（第二段是独立进程，不继承覆盖确认）。
func emitAppRegistration(w io.Writer, code *feishu.AppRegistrationCode, asJSON, showResumeHint, withForce bool) {
	resume := ""
	if showResumeHint {
		resume = "room init --device-code " + code.DeviceCode
		if withForce {
			resume += " --force"
		}
	}
	if asJSON {
		_ = output.WriteSuccess(w, struct {
			Event                   string `json:"event"`
			DeviceCode              string `json:"device_code"`
			UserCode                string `json:"user_code"`
			VerificationURI         string `json:"verification_uri"`
			VerificationURIComplete string `json:"verification_uri_complete"`
			ExpiresIn               int    `json:"expires_in"`
			Interval                int    `json:"interval"`
			ResumeCommand           string `json:"resume_command,omitempty"`
		}{"app_registration", code.DeviceCode, code.UserCode, code.VerificationURI, code.VerificationURIComplete, code.ExpiresInSec, code.IntervalSec, resume}, nil)
		return
	}
	fmt.Fprintln(w, "请在浏览器中打开以下链接，登录飞书并确认创建个人应用：")
	fmt.Fprintf(w, "\n    %s\n\n", code.VerificationURIComplete)
	if code.UserCode != "" {
		fmt.Fprintf(w, "如页面提示输入验证码，请输入：%s\n", code.UserCode)
		if code.VerificationURI != "" {
			fmt.Fprintf(w, "（或手动访问 %s 并输入上述验证码）\n", code.VerificationURI)
		}
	}
	if resume != "" {
		fmt.Fprintf(w, "\n设备码 device_code：%s\n", code.DeviceCode)
		fmt.Fprintln(w, "授权完成后运行以下命令换取并保存应用凭证（适合 agent/CI/无头环境两段式）：")
		fmt.Fprintf(w, "    %s\n", resume)
	}
}

// saveAppCredentials 把注册到的应用凭证写入全局 config.toml 并输出结果；
// stdout 绝不打印明文 secret。
func saveAppCredentials(w io.Writer, path string, creds *feishu.AppCredentials, asJSON bool) error {
	if err := saveCredsTOML(path, creds); err != nil {
		// 此时应用已在飞书侧创建，app_id 可打印（非机密），secret 只能去开发者后台找回
		return fmt.Errorf("写入 %s 失败: %w（新应用 %s 已创建，app_secret 可在 open.feishu.cn 开发者后台查看）", path, err, creds.AppID)
	}

	if asJSON {
		return output.WriteSuccess(w, struct {
			Event       string `json:"event"`
			AppID       string `json:"app_id"`
			OpenID      string `json:"open_id"`
			TenantBrand string `json:"tenant_brand"`
			EnvPath     string `json:"env_path"` // 兼容字段：历史消费者按此取写入路径
			Dest        string `json:"dest"`     // 恒为 "toml"
		}{"app_registered", creds.AppID, creds.OpenID, creds.TenantBrand, path, "toml"}, nil)
	}
	fmt.Fprintf(w, "\n应用创建成功！app_id: %s（app_secret 已写入 %s，不在终端显示）\n", creds.AppID, path)
	fmt.Fprintln(w, "请运行 room login 完成用户授权")
	return nil
}

// saveCredsTOML 凭证写入全局 config.toml（保留文件中其他配置项）。
func saveCredsTOML(path string, creds *feishu.AppCredentials) error {
	doc, err := config.ReadFile(path)
	if err != nil {
		return err
	}
	doc.Values["FEISHU_APP_ID"] = creds.AppID
	doc.Values["FEISHU_APP_SECRET"] = creds.AppSecret
	return config.WriteFile(path, doc)
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

// loginOptions 汇总 login 命令的 flag（机读输出走全局 --json）。
type loginOptions struct {
	noWait     bool   // 仅发起授权并打印后立即返回、不轮询（agent/CI 两段式）
	deviceCode string // 用前一次 --no-wait 得到的设备码恢复轮询
	jsonOut    bool   // 由全局 --json 填充
}

func newLoginCmd(a *app) *cobra.Command {
	opts := loginOptions{}
	cmd := &cobra.Command{
		Use:   "login",
		Short: "通过飞书 OAuth 设备码流程授权，保存用户身份凭证（用于以本人身份预定）",
		Long: `OAuth 设备码流程授权用户身份，凭证存于配置目录旁的 cache/feishu-user-token.json。

三种模式（与 init 同构）：
  默认      发起授权 → 打印授权链接（TTY 下尽力打开浏览器）→ 原地轮询到授权完成
  --no-wait 仅发起并打印 device_code 与恢复命令后立即返回（agent/CI 两段式第一步）
  --device-code <code>  用户授权后恢复轮询，保存用户凭证（两段式第二步）

--json 时输出 device_authorization / login_ok 两种事件信封（不含 token 明文）。
前置条件：已有应用凭证（先 room init）。`,
		Example: `  room login
  room login --no-wait --json
  room login --device-code <code> --json`,
		Args: validationArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateDeviceCodeFlag(cmd.Flags().Changed("device-code"), opts.deviceCode); err != nil {
				return err
			}
			opts.jsonOut = a.jsonOut
			return a.runLogin(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&opts.noWait, "no-wait", false, "仅发起授权并打印 device_code 后返回，不轮询（agent/CI 两段式）")
	fl.StringVar(&opts.deviceCode, "device-code", "", "用前一次 --no-wait 得到的设备码恢复轮询")
	return cmd
}

// runLogin 三种模式（与 init 的设备码流程同构）：
//   - 默认（阻塞）：发起授权 → 展示授权链接（TTY 下尽力打开浏览器）→ 原地轮询到授权完成。
//   - --no-wait：仅发起授权并打印（含 device_code + 恢复命令）后立即返回。
//   - --device-code <code>：跳过发起，用已有设备码恢复轮询换取并保存用户凭证。
func (a *app) runLogin(ctx context.Context, w io.Writer, opts loginOptions) error {
	if err := validateResumeFlags(opts.noWait, opts.deviceCode); err != nil {
		return err
	}
	appID := env("FEISHU_APP_ID")
	appSecret := env("FEISHU_APP_SECRET")
	if appID == "" || appSecret == "" {
		return output.Errf(output.TypeConfig,
			"运行 room init 自动创建应用，或 room config set feishu.app_id / feishu.app_secret 手动写入",
			"缺失飞书应用凭证（FEISHU_APP_ID / FEISHU_APP_SECRET）")
	}

	httpClient := feishu.NewHTTPClient()
	auth := &feishu.Auth{
		Mode:        feishu.AuthModeAuto,
		TokenClient: &feishu.OAuthClient{HTTP: httpClient, AppID: appID, AppSecret: appSecret},
		Store:       &feishu.FileUserTokenStore{Path: userTokenPath()},
		Clock:       a.now,
		Log:         a.log,
	}

	// 恢复轮询模式：用已有 device_code 直接轮询换取用户凭证
	if opts.deviceCode != "" {
		if !opts.jsonOut {
			fmt.Fprintln(w, "正在轮询授权结果...（在浏览器完成授权后将自动继续）")
		}
		token, err := feishu.PollDeviceLogin(ctx, auth, opts.deviceCode, resumePollIntervalSec, resumePollExpiresInSec)
		if err != nil {
			return classifyLoginErr(err)
		}
		return emitLoginOK(w, token, opts.jsonOut)
	}

	device, err := auth.TokenClient.RequestDeviceAuthorization(ctx, userAuthScope)
	if err != nil {
		return output.Wrap(output.TypeAPI, "检查应用凭证是否有效后重试",
			fmt.Errorf("发起设备码授权失败: %w", err))
	}
	if err := rejectTaintedDeviceCode(device.DeviceCode); err != nil {
		return err
	}

	// 发起-即返回模式：打印设备码供后续 --device-code 恢复，不阻塞轮询
	if opts.noWait {
		emitDeviceAuthorization(w, device, opts.jsonOut, true)
		return nil
	}

	// 默认阻塞模式：展示授权入口 + TTY 下尽力打开浏览器 + 原地轮询到完成
	emitDeviceAuthorization(w, device, opts.jsonOut, false)
	if a.streams.OutIsTerminal {
		feishu.OpenBrowser(deviceVerifyURL(device))
	}
	if !opts.jsonOut {
		fmt.Fprintln(w, "\n等待授权中...（在浏览器完成授权后将自动继续）")
	}
	token, err := feishu.PollDeviceLogin(ctx, auth, device.DeviceCode, device.IntervalSec, device.ExpiresInSec)
	if err != nil {
		return classifyLoginErr(err)
	}
	return emitLoginOK(w, token, opts.jsonOut)
}

// classifyLoginErr 登录轮询错误归类：过期可重试，其余归 auth。
func classifyLoginErr(err error) error {
	if errors.Is(err, feishu.ErrLoginExpired) {
		return output.Wrap(output.TypeAuth, "重新运行 room login 并在有效期内完成授权", err).WithRetryable()
	}
	return output.Wrap(output.TypeAuth, "重新运行 room login", fmt.Errorf("用户授权失败: %w", err))
}

func deviceVerifyURL(device *feishu.DeviceAuthorization) string {
	if device.VerificationURIComplete != "" {
		return device.VerificationURIComplete
	}
	return device.VerificationURI
}

// emitDeviceAuthorization 输出登录授权入口；showResumeHint 为 true 时附上
// --device-code 恢复命令（--no-wait 用）。
func emitDeviceAuthorization(w io.Writer, device *feishu.DeviceAuthorization, asJSON, showResumeHint bool) {
	resume := ""
	if showResumeHint {
		resume = "room login --device-code " + device.DeviceCode
	}
	if asJSON {
		_ = output.WriteSuccess(w, struct {
			Event                   string `json:"event"`
			DeviceCode              string `json:"device_code"`
			UserCode                string `json:"user_code"`
			VerificationURI         string `json:"verification_uri"`
			VerificationURIComplete string `json:"verification_uri_complete"`
			ExpiresIn               int    `json:"expires_in"`
			Interval                int    `json:"interval"`
			ResumeCommand           string `json:"resume_command,omitempty"`
		}{"device_authorization", device.DeviceCode, device.UserCode, device.VerificationURI, device.VerificationURIComplete, device.ExpiresInSec, device.IntervalSec, resume}, nil)
		return
	}
	fmt.Fprintln(w, "请在【已登录飞书的浏览器 / 飞书客户端】中打开以下链接并点击「同意」授权：")
	fmt.Fprintf(w, "\n    %s\n\n", deviceVerifyURL(device))
	if device.VerificationURIComplete == "" && device.UserCode != "" {
		fmt.Fprintf(w, "用户码 user_code：%s\n", device.UserCode)
	}
	if resume != "" {
		fmt.Fprintf(w, "设备码 device_code：%s\n", device.DeviceCode)
		fmt.Fprintln(w, "授权完成后运行以下命令换取并保存用户凭证（适合 agent/CI/无头环境两段式）：")
		fmt.Fprintf(w, "    %s\n", resume)
	}
}

// emitLoginOK 登录成功输出；绝不打印 token 明文。
func emitLoginOK(w io.Writer, token *feishu.StoredUserToken, asJSON bool) error {
	if asJSON {
		return output.WriteSuccess(w, struct {
			Event          string `json:"event"`
			OpenID         string `json:"open_id,omitempty"`
			UserID         string `json:"user_id,omitempty"`
			Name           string `json:"name,omitempty"`
			Scope          string `json:"scope,omitempty"`
			AuthExpireAtMs int64  `json:"auth_expire_at_ms,omitempty"`
		}{"login_ok", token.OpenID, token.UserID, token.Name, token.Scope, token.AuthExpireAt}, nil)
	}
	who := ""
	if token.Name != "" {
		who = fmt.Sprintf("（%s）", token.Name)
	}
	fmt.Fprintf(w, "登录成功%s，已保存用户凭证，后续预定将优先以用户身份执行\n", who)
	return nil
}

const notifyTestMessage = "来自 room 的测试消息"

func newNotifyCmd(a *app) *cobra.Command {
	var test bool
	cmd := &cobra.Command{
		Use:   "notify [text]",
		Short: "通过配置的飞书自定义机器人 webhook 发送文本消息（无需应用权限/机器人能力）",
		Long: `发送文本消息到 notify.webhook 配置的自定义机器人。

消息文本必填；验证 webhook 链路用 --test 发送固定测试消息（与消息文本互斥）。`,
		Example: `  room notify "会议室已预订：3F-A 14:00-15:00"
  room notify --test           # 验证 webhook 链路
  room notify "构建完成" --json`,
		Args: validationArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			message := ""
			if len(args) > 0 {
				message = strings.TrimSpace(args[0])
			}
			// 空消息静默发测试文案是脚本陷阱（notify "$MSG" 变量意外为空即误发），
			// 测试消息必须显式 --test
			switch {
			case test && message != "":
				return output.Errf(output.TypeValidation,
					"--test 与消息文本互斥，二选一", "--test 不接受消息文本参数")
			case test:
				message = notifyTestMessage
			case message == "":
				return output.Errf(output.TypeValidation,
					"提供消息文本，或用 room notify --test 发送测试消息", "缺少消息文本")
			}

			url := env("FEISHU_BOT_WEBHOOK")
			if url == "" {
				return output.Errf(output.TypeConfig,
					"运行 room config set notify.webhook <地址> 设置自定义机器人 webhook",
					"缺失 FEISHU_BOT_WEBHOOK 配置")
			}

			client := &feishu.WebhookClient{
				URL:    url,
				Secret: env("FEISHU_BOT_WEBHOOK_SECRET"),
				Post:   feishu.NewWebhookPoster(feishu.NewHTTPClient()),
				Clock:  a.now,
			}
			if err := client.SendText(message); err != nil {
				return output.Wrap(output.TypeAPI, "检查 notify.webhook / notify.webhook_secret 配置后重试",
					fmt.Errorf("发送 webhook 消息失败: %w", err))
			}
			if a.jsonOut {
				return output.WriteSuccess(cmd.OutOrStdout(), struct {
					Sent    bool   `json:"sent"`
					Message string `json:"message"`
				}{true, message}, nil)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "已发送: %s\n", message)
			return nil
		},
	}
	cmd.Flags().BoolVar(&test, "test", false, "发送固定测试消息验证 webhook 链路（与消息文本互斥）")
	return cmd
}

var (
	simpleTimeRegex = regexp.MustCompile(`^([0-1][0-9]|2[0-3]):[0-5][0-9]-([0-1][0-9]|2[0-3]):[0-5][0-9]$`)
	fullTimeRegex   = regexp.MustCompile(`^([0-1][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]-([0-1][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]$`)
	shortDateRegex  = regexp.MustCompile(`^\d{1,2}-\d{1,2}$`)
)

// expandParticipants 展开 -p 的每个值（值内空格分隔多个），兼容 -p "alice bob" 旧用法。
func expandParticipants(vals []string) []string {
	var out []string
	for _, v := range vals {
		out = append(out, strings.Fields(v)...)
	}
	return out
}

func newBookCmd(a *app) *cobra.Command {
	var date, timeRange, titleFlag string
	var participants []string
	var yes bool

	cmd := &cobra.Command{
		Use:   "book [input]",
		Short: "智能预订会议室(支持自然语言输入和传统参数)",
		Long: `三种输入方式：
  1. 自然语言：room book "tom 2pm 1h shikai 团队周会"（需配置 nlp.api_key）
  2. 显式参数：room book -d 07-15 -t 14:00-15:00 --title 周会
  3. 交互问答：room book（仅终端环境）

标题优先级：--title > 自然语言解析 > 交互问答/默认值（meeting）。

注意：
  - book 总是真实预订，不支持 --dryrun（传入会 exit 2，演练仅 auto 支持）
  - --json/非终端环境（stdin 或 stdout 任一被 pipe/重定向）必须给全 -d 与 -t，
    缺失立即 exit 2 而非挂起
  - exit 0 ⟺ 房间订上了；未订到 exit 1，错误 type 区分：
    no_room（无可用会议室）/ conflict（时段已有日程）/ holiday_skipped（节假日跳过）
    / no_participants（无有效参会人，运行 room login 或补 -p）
  - room login 的授权用户会自动加入参会人（无需在 -p 中重复）`,
		Example: `  room book "明天下午3点 开发周会"
  room book -d 07-15 -t 14:00-15:00 -p "alice bob" --title 架构评审 --json
  room book -d 07-15 -t 14:00-15:00 -y`,
		Args: validationArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p := a.newPrompter()

			var dateToBook, startTime, endTime string
			title := ""

			nlInput := ""
			if len(args) > 0 {
				nlInput = args[0]
			}
			if nlInput == "" && date == "" && timeRange == "" {
				var err error
				nlInput, err = p.questionRequired(
					`请输入会议安排 (如: "tom 2pm 1h shikai 团队周会" / "明天下午3点" / "6pm 30m"): `,
					"缺少预订信息",
					"提供自然语言输入或显式参数：room book -d MM-DD -t HH:MM-HH:MM [--title ...]")
				if err != nil {
					return err
				}
			}

			if nlInput != "" {
				parser := a.newNLPParser()
				parsed, err := parser.ParseOneShotWithDefaults(ctx, nlInput)
				switch {
				case err != nil && !p.interactive:
					return output.Wrap(output.TypeValidation,
						"改用显式参数：room book -d MM-DD -t HH:MM-HH:MM --title ...；或检查 nlp.api_key 配置",
						fmt.Errorf("自然语言解析失败: %w", err))
				case err != nil:
					a.log.Warn(fmt.Sprintf("自然语言解析失败，使用交互式输入兜底: %v", err))
				default:
					a.log.Info(fmt.Sprintf("自然语言解析成功: date=%s, startTime=%s, endTime=%s, duration=%dmin, title=%s, participants=[%s]",
						parsed.Date, parsed.StartTime, parsed.EndTime, parsed.Duration, parsed.Title,
						strings.Join(parsed.Participants, ", ")))
					dateToBook = parsed.Date
					startTime = parsed.StartTime
					endTime = parsed.EndTime
					title = parsed.Title
					if len(parsed.Participants) > 0 {
						participants = parsed.Participants
					}
				}
			}

			if dateToBook == "" || startTime == "" || endTime == "" {
				dateToBook = date
				if dateToBook == "" {
					var err error
					dateToBook, err = p.questionRequired("请输入会议日期 (MM-DD or YYYY-MM-DD): ",
						"缺少会议日期", "传入 -d MM-DD 或 -d YYYY-MM-DD")
					if err != nil {
						return err
					}
					if dateToBook == "" {
						dateToBook = a.now().In(a.loc).Format("2006-01-02")
						a.log.Info(fmt.Sprintf("使用默认日期: %s", dateToBook))
					}
				}

				if shortDateRegex.MatchString(dateToBook) {
					dateToBook = fmt.Sprintf("%s-%s", a.now().In(a.loc).Format("2006"), dateToBook)
				}
				normalized, err := normalizeDate(dateToBook, a.loc)
				if err != nil {
					return output.Errf(output.TypeValidation, "使用 MM-DD 或 YYYY-MM-DD 格式", "无效的日期格式: %s", dateToBook)
				}
				dateToBook = normalized

				timeInput := timeRange
				if timeInput == "" {
					timeInput, err = p.questionRequired("请输入会议时间范围 (HH:MM-HH:MM or HH:MM:SS-HH:MM:SS): ",
						"缺少会议时间范围", "传入 -t HH:MM-HH:MM")
					if err != nil {
						return err
					}
					if timeInput == "" {
						return output.Errf(output.TypeValidation, "传入 -t HH:MM-HH:MM", "时间不能为空")
					}
				}

				switch {
				case simpleTimeRegex.MatchString(timeInput):
					parts := strings.SplitN(timeInput, "-", 2)
					startTime = parts[0] + ":00"
					endTime = parts[1] + ":00"
				case fullTimeRegex.MatchString(timeInput):
					parts := strings.SplitN(timeInput, "-", 2)
					startTime = parts[0]
					endTime = parts[1]
				default:
					return output.Errf(output.TypeValidation, "使用 HH:MM-HH:MM 或 HH:MM:SS-HH:MM:SS 格式", "无效的时间格式: %s", timeInput)
				}
			}

			// 过去时间检测：交互环境让用户选择推迟或取消；
			// 非交互不静默改期（对 agent 静默改日期是危险副作用），fail-fast 让调用方显式换日重试。
			explicitArgs := nlInput == "" && date != "" && timeRange != ""
			if !explicitArgs && nlp.IsPastMeetingTime(dateToBook, startTime, a.now().In(a.loc), a.loc) {
				if !p.interactive {
					return output.Errf(output.TypeValidation,
						"时间已过，请显式指定未来日期：room book -d <日期> -t <时间段>",
						"解析出的时间 %s %s 已过当前时间", dateToBook, startTime).
						WithDetail(map[string]string{"parsed_date": dateToBook, "start_time": startTime})
				}
				parsedDate, _ := time.ParseInLocation("2006-01-02", dateToBook, a.loc)
				tomorrow := parsedDate.AddDate(0, 0, 1).Format("2006-01-02")
				nextWeek := parsedDate.AddDate(0, 0, 7).Format("2006-01-02")
				answer := p.question(fmt.Sprintf(
					"解析出的时间 %s %s 已过当前时间。\n  1) 明天 %s %s\n  2) 下周 %s %s\n  3) 取消\n请选择 [1]: ",
					dateToBook, startTime, tomorrow, startTime, nextWeek, startTime))

				choice := nlp.ChoiceTomorrow
				switch answer {
				case "", "1":
					choice = nlp.ChoiceTomorrow
				case "2":
					choice = nlp.ChoiceNextWeek
				default:
					choice = nlp.ChoiceCancel
				}

				newDate := nlp.ShiftMeetingDate(dateToBook, choice, a.loc)
				if newDate == "" {
					a.log.Info("用户取消了预订")
					return nil
				}
				dateToBook = newDate
				a.log.Info(fmt.Sprintf("已将会议日期调整为: %s", dateToBook))
			}

			if len(participants) == 0 && !yes {
				if v := p.questionOptional("请输入参与者列表（邮箱前缀或群ID），用空格分隔 (可选，直接回车跳过): "); v != "" {
					participants = []string{v}
				}
			}

			// 标题优先级：--title > 自然语言解析 > 交互问答/默认值
			switch {
			case titleFlag != "":
				title = titleFlag
			case title == "":
				if yes {
					title = defaultMeetingTitle
				} else {
					title = p.questionOptional(fmt.Sprintf("请输入会议标题(默认为 %q): ", defaultMeetingTitle))
				}
				if title == "" {
					title = defaultMeetingTitle
				}
			}

			// book 总是真实预订（--dryrun 在 PersistentPreRunE 已被拒），
			// 传 true 会让启动日志误报「模拟模式」
			service, err := a.newService(ctx, false)
			if err != nil {
				return err
			}

			result, err := service.BookRoom(ctx, dateToBook, startTime, endTime, title, expandParticipants(participants))
			if err != nil {
				return err
			}
			return a.emitBookResult(cmd.OutOrStdout(), result)
		},
	}

	cmd.Flags().StringVarP(&date, "date", "d", "", "会议日期 (MM-DD or YYYY-MM-DD)")
	cmd.Flags().StringVarP(&timeRange, "time", "t", "", "会议时间范围 (HH:MM-HH:MM or HH:MM:SS-HH:MM:SS)")
	cmd.Flags().StringArrayVarP(&participants, "participants", "p", nil, "参与者：邮箱前缀、完整邮箱或 oc_ 群聊 ID（可重复传参；单个值内也可用空格分隔多个）")
	cmd.Flags().StringVar(&titleFlag, "title", "", "会议标题（优先于自然语言解析结果）")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "跳过参会人、标题等可选交互，使用默认值")
	return cmd
}

// emitBookResult 预订结果落盘：booked 输出到 stdout（exit 0）；
// 未订到映射为对应类型的错误信封（exit 1），保证 exit 0 ⟺ 房间订上了。
func (a *app) emitBookResult(w io.Writer, r *booking.BookResult) error {
	timeDetail := map[string]string{"date": r.Date, "start_time": r.StartTime, "end_time": r.EndTime}
	switch r.Status {
	case booking.StatusBooked:
		if a.jsonOut {
			return output.WriteSuccess(w, r, nil)
		}
		fmt.Fprintf(w, "已预订: %s %s %s-%s (event_id: %s)\n", r.Room.Name, r.Date, r.StartTime, r.EndTime, r.EventID)
		return nil
	case booking.StatusHolidaySkipped:
		return output.Errf(output.TypeHolidaySkipped, "更换非节假日日期重试",
			"%s 为节假日，按策略跳过预订", r.Date).WithDetail(timeDetail)
	case booking.StatusConflict:
		return output.Errf(output.TypeConflict, "运行 room list --json 查看已有日程后更换时间",
			"时间段已有日历事件重叠: %s %s-%s", r.Date, r.StartTime, r.EndTime).WithDetail(timeDetail)
	case booking.StatusNoRoom:
		return output.Errf(output.TypeNoRoom,
			"更换时间段重试，或调整 booking.room_list / booking.room_size / booking.room_level_id 配置",
			"未找到可用会议室: %s %s-%s", r.Date, r.StartTime, r.EndTime).WithDetail(timeDetail)
	case booking.StatusNoParticipants:
		return output.Errf(output.TypeNoParticipants,
			"运行 room login 让本人自动加入参会人；或用 -p 提供可解析的参会人（检查 booking.email_domain 配置）",
			"无有效参会人，已放弃预订: %s %s-%s", r.Date, r.StartTime, r.EndTime).
			WithDetail(map[string]any{"date": r.Date, "start_time": r.StartTime,
				"end_time": r.EndTime, "participants_unresolved": r.ParticipantsUnresolved})
	default:
		// BookRoom 契约外的状态（planned/failed 或未来新增值）说明代码失同步，
		// 报 internal 而非误导性的 no_room
		return output.Errf(output.TypeInternal, "请升级 room 或反馈该问题",
			"未预期的预订状态: %s", r.Status).WithDetail(timeDetail)
	}
}

// normalizeDate 校验并归一化日期为 YYYY-MM-DD。
func normalizeDate(date string, loc *time.Location) (string, error) {
	t, err := time.ParseInLocation("2006-01-02", date, loc)
	if err == nil {
		return t.Format("2006-01-02"), nil
	}
	t, err = time.ParseInLocation("2006-1-2", date, loc)
	if err != nil {
		return "", err
	}
	return t.Format("2006-01-02"), nil
}

var _ = context.Background
