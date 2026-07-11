package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/amzyang/room/booking"
	"github.com/amzyang/room/config"
	"github.com/amzyang/room/feishu"
	"github.com/amzyang/room/nlp"
)

func newAutoCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "auto",
		Short: "根据 TASK_FORMAT 自动预订会议室",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			service, err := a.newBookingService(ctx, !a.run)
			if err != nil {
				return err
			}
			return service.AutoBook(ctx, !a.run)
		},
	}
}

func newListCmd(a *app) *cobra.Command {
	var days int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出未来的日历事件",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			service, err := a.newBookingService(ctx, false)
			if err != nil {
				return err
			}
			events, err := service.ListMyEvents(ctx, days, false)
			if err != nil {
				return err
			}
			fmt.Print(booking.FormatEventList(events, days))
			return nil
		},
	}
	cmd.Flags().IntVarP(&days, "days", "d", defaultCancelDays, "显示未来几天的事件")
	return cmd
}

func newCancelCmd(a *app) *cobra.Command {
	var days int
	cmd := &cobra.Command{
		Use:   "cancel",
		Short: "列出并取消日历事件",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			service, err := a.newBookingService(ctx, false)
			if err != nil {
				return err
			}
			events, err := service.ListMyEvents(ctx, days, true)
			if err != nil {
				return err
			}

			fmt.Print(booking.FormatCancelList(events, days))
			if len(events) == 0 {
				return nil
			}

			p := newPrompter()
			choice := p.question(fmt.Sprintf("请输入要取消的事件编号(1 - %d)，或按回车键取消操作: ", len(events)))
			if choice == "" {
				fmt.Println("已取消操作")
				return nil
			}
			index, err := strconv.Atoi(choice)
			if err != nil || index < 1 || index > len(events) {
				fmt.Println("无效的编号")
				return nil
			}

			selected := events[index-1]
			fmt.Println("\n您选择取消的事件:")
			fmt.Printf("标题: %s\n", selected.Title)
			fmt.Printf("时间: %s - %s\n", selected.StartTime, selected.EndTime)
			location := selected.Location
			if location == "" {
				location = "无"
			}
			fmt.Printf("地点: %s\n", location)

			confirm := strings.ToLower(p.question("\n确认取消此事件吗？(y/N): "))
			if confirm != "y" && confirm != "yes" {
				fmt.Println("已取消操作")
				return nil
			}

			message, err := service.CancelEvent(ctx, selected.EventID)
			if err != nil {
				return err
			}
			fmt.Printf("✅ %s\n", message)
			return nil
		},
	}
	cmd.Flags().IntVarP(&days, "days", "d", defaultCancelDays, "显示未来几天的事件")
	return cmd
}

const (
	// --device-code 恢复轮询时 begin 的 interval/expires_in 已丢失，取保守值
	initResumePollIntervalSec  = 5
	initResumePollExpiresInSec = 600
)

// initOptions 汇总 init 命令的 flag。
type initOptions struct {
	force      bool   // 已有应用凭证时允许覆盖（同时撤销并清除已保存的登录凭证）
	noWait     bool   // 仅发起注册并打印后立即返回、不轮询（供 agent/CI/无头环境两段式）
	deviceCode string // 用前一次 --no-wait 得到的设备码恢复轮询
	jsonOut    bool   // 输出机读 JSON（app_registration / app_registered 事件）
}

// newInitCmd 构造 init 命令：匿名 device flow 自动创建飞书 PersonalAgent 个人应用
// 并把凭证写入全局 config.toml，替代去开放平台手动建应用抄 app_id/app_secret。
func newInitCmd(a *app) *cobra.Command {
	opts := initOptions{}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "一键自动创建飞书 PersonalAgent 个人应用并把凭证写入配置",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// agent 脚本取值失败易传出空串，静默降级为全新注册会白等一轮授权
			if cmd.Flags().Changed("device-code") && opts.deviceCode == "" {
				return fmt.Errorf("--device-code 不能为空")
			}
			return a.runInit(cmd.Context(), opts)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&opts.force, "force", false, "覆盖已有应用凭证（同时撤销并清除已保存的登录凭证）")
	fl.BoolVar(&opts.noWait, "no-wait", false, "仅发起注册并打印 device_code 后返回，不轮询（agent/CI 两段式）")
	fl.StringVar(&opts.deviceCode, "device-code", "", "用前一次 --no-wait 得到的设备码恢复轮询")
	fl.BoolVar(&opts.jsonOut, "json", false, "输出机读 JSON 事件（app_registration / app_registered）")
	return cmd
}

// runInit 三种模式（与 login 的设备码流程同构）：
//   - 默认（阻塞）：发起注册 → 展示授权链接（尽力打开浏览器）→ 原地轮询到应用创建完成。
//   - --no-wait：仅发起注册并打印（含 device_code + 恢复命令）后立即返回。
//   - --device-code <code>：跳过发起，用已有设备码恢复轮询换取并保存凭证。
func (a *app) runInit(ctx context.Context, opts initOptions) error {
	if err := validateInitOptions(opts); err != nil {
		return err
	}
	// 覆盖保护在任何网络请求之前，begin 与 --device-code 恢复两条路径都要过
	oldAppID, oldAppSecret := env("FEISHU_APP_ID"), env("FEISHU_APP_SECRET")
	if err := checkExistingAppCredentials(oldAppID, oldAppSecret, opts.force); err != nil {
		return err
	}

	tokenPath := env("FEISHU_USER_TOKEN_PATH")
	if tokenPath == "" {
		tokenPath = defaultUserTokenPath
	}
	reg := &feishu.AppRegistrar{HTTP: feishu.NewHTTPClient(tlsInsecure()), Log: a.log, Clock: a.now}

	// 恢复轮询模式：用已有 device_code 直接轮询换取应用凭证
	if opts.deviceCode != "" {
		if !opts.jsonOut {
			fmt.Println("正在轮询应用创建结果...（在浏览器完成授权后将自动继续）")
		}
		creds, err := reg.Poll(ctx, opts.deviceCode, initResumePollIntervalSec, initResumePollExpiresInSec)
		if err != nil {
			return err
		}
		return a.finishInit(ctx, creds, oldAppID, oldAppSecret, tokenPath, opts.jsonOut)
	}

	code, err := reg.Begin(ctx)
	if err != nil {
		return fmt.Errorf("自动创建应用失败（%w）。可改用手动方式：在 open.feishu.cn 创建应用后用 room config set 写入凭证", err)
	}

	// 发起-即返回模式：打印设备码供后续 --device-code 恢复，不阻塞轮询
	if opts.noWait {
		emitAppRegistration(os.Stdout, code, opts.jsonOut, true, opts.force)
		return nil
	}

	// 默认阻塞模式：展示授权入口 + 尽力打开浏览器 + 原地轮询到完成
	emitAppRegistration(os.Stdout, code, opts.jsonOut, false, opts.force)
	feishu.OpenBrowser(code.VerificationURIComplete)
	if !opts.jsonOut {
		fmt.Println("\n等待授权中...（在浏览器完成授权后将自动继续）")
	}
	creds, err := reg.Poll(ctx, code.DeviceCode, code.IntervalSec, code.ExpiresInSec)
	if err != nil {
		return err
	}
	return a.finishInit(ctx, creds, oldAppID, oldAppSecret, tokenPath, opts.jsonOut)
}

// finishInit 注册成功后的收尾：写凭证 → 撤销旧应用登录凭证 → 提示覆盖风险。
// 先落盘再撤销：写入失败时保留旧登录态，避免「新应用已创建、旧登录又被销毁」的双输局面。
func (a *app) finishInit(ctx context.Context, creds *feishu.AppCredentials, oldAppID, oldAppSecret, tokenPath string, jsonOut bool) error {
	if err := saveAppCredentials(os.Stdout, a.cfg.Path, creds, jsonOut); err != nil {
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

// validateInitOptions 校验互斥的 flag 组合。
func validateInitOptions(opts initOptions) error {
	if opts.noWait && opts.deviceCode != "" {
		return fmt.Errorf("--no-wait 与 --device-code 互斥：前者发起注册，后者恢复轮询")
	}
	return nil
}

// checkExistingAppCredentials 保护已有应用凭证：覆盖需显式 --force（并意味着清除旧应用的登录凭证）。
func checkExistingAppCredentials(appID, appSecret string, force bool) error {
	if force {
		return nil
	}
	if appID != "" || appSecret != "" {
		return fmt.Errorf("环境变量或 config.toml 已有应用凭证（app_id %s）。如需替换为新应用请加 --force（将同时撤销并清除已保存的登录凭证，需重新 room login）", maskSecret(appID))
	}
	return nil
}

// revokeOldLoginBestEffort 覆盖旧凭证前撤销旧应用的远端 token（失败仅告警），
// 并无条件删除本地用户凭证文件——旧 token 属旧应用，残留会让新应用误带失效凭证。
func (a *app) revokeOldLoginBestEffort(ctx context.Context, oldAppID, oldAppSecret, tokenPath string) {
	store := &feishu.FileUserTokenStore{Path: tokenPath}
	if oldAppID != "" && oldAppSecret != "" {
		client := &feishu.OAuthClient{HTTP: feishu.NewHTTPClient(tlsInsecure()), AppID: oldAppID, AppSecret: oldAppSecret}
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
		printJSON(w, struct {
			Event                   string `json:"event"`
			DeviceCode              string `json:"device_code"`
			UserCode                string `json:"user_code"`
			VerificationURI         string `json:"verification_uri"`
			VerificationURIComplete string `json:"verification_uri_complete"`
			ExpiresIn               int    `json:"expires_in"`
			Interval                int    `json:"interval"`
			ResumeCommand           string `json:"resume_command,omitempty"`
		}{"app_registration", code.DeviceCode, code.UserCode, code.VerificationURI, code.VerificationURIComplete, code.ExpiresInSec, code.IntervalSec, resume})
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
		printJSON(w, struct {
			Event       string `json:"event"`
			AppID       string `json:"app_id"`
			OpenID      string `json:"open_id"`
			TenantBrand string `json:"tenant_brand"`
			EnvPath     string `json:"env_path"` // 兼容字段：历史消费者按此取写入路径
			Dest        string `json:"dest"`     // 恒为 "toml"
		}{"app_registered", creds.AppID, creds.OpenID, creds.TenantBrand, path, "toml"})
		return nil
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

func printJSON(w io.Writer, v any) {
	data, _ := json.Marshal(v)
	fmt.Fprintln(w, string(data))
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

func newLoginCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "通过飞书 OAuth 设备码流程授权，保存用户身份凭证（用于以本人身份预定）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			appID := env("FEISHU_APP_ID")
			appSecret := env("FEISHU_APP_SECRET")
			if appID == "" || appSecret == "" {
				return fmt.Errorf("飞书配置缺失，请检查 FEISHU_APP_ID 和 FEISHU_APP_SECRET 环境变量")
			}

			scope := env("FEISHU_USER_AUTH_SCOPE")
			if scope == "" {
				scope = defaultUserAuthScope
			}
			userTokenPath := env("FEISHU_USER_TOKEN_PATH")
			if userTokenPath == "" {
				userTokenPath = defaultUserTokenPath
			}

			httpClient := feishu.NewHTTPClient(tlsInsecure())
			auth := &feishu.Auth{
				Mode:        feishu.AuthModeAuto,
				TokenClient: &feishu.OAuthClient{HTTP: httpClient, AppID: appID, AppSecret: appSecret},
				Store:       &feishu.FileUserTokenStore{Path: userTokenPath},
				Clock:       a.now,
				Log:         a.log,
			}
			if _, err := feishu.RunDeviceLogin(cmd.Context(), auth, scope); err != nil {
				return fmt.Errorf("用户授权失败: %w", err)
			}
			a.log.Info("已保存用户凭证，后续预定将优先以用户身份执行")
			return nil
		},
	}
}

func newNotifyCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "notify [text]",
		Short: "通过配置的飞书自定义机器人 webhook 发送文本消息（无需应用权限/机器人能力）",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			url := env("FEISHU_BOT_WEBHOOK")
			if url == "" {
				return fmt.Errorf("缺失 FEISHU_BOT_WEBHOOK 配置，请用 room config set notify.webhook 设置自定义机器人 webhook 地址")
			}

			message := ""
			if len(args) > 0 {
				message = strings.TrimSpace(args[0])
			}
			if message == "" {
				message = "来自 room 的测试消息"
			}

			client := &feishu.WebhookClient{
				URL:    url,
				Secret: env("FEISHU_BOT_WEBHOOK_SECRET"),
				Post:   feishu.NewWebhookPoster(feishu.NewHTTPClient(tlsInsecure())),
				Clock:  a.now,
			}
			if err := client.SendText(message); err != nil {
				return fmt.Errorf("发送 webhook 消息失败: %w", err)
			}
			a.log.Info(fmt.Sprintf("已通过 webhook 发送消息: %s", message))
			return nil
		},
	}
}

var (
	simpleTimeRegex = regexp.MustCompile(`^([0-1][0-9]|2[0-3]):[0-5][0-9]-([0-1][0-9]|2[0-3]):[0-5][0-9]$`)
	fullTimeRegex   = regexp.MustCompile(`^([0-1][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]-([0-1][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]$`)
	shortDateRegex  = regexp.MustCompile(`^\d{1,2}-\d{1,2}$`)
)

func newBookCmd(a *app) *cobra.Command {
	var date, timeRange, participants string
	var yes bool

	cmd := &cobra.Command{
		Use:   "book [input]",
		Short: "智能预订会议室(支持自然语言输入和传统参数)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p := newPrompter()

			var dateToBook, startTime, endTime string
			title := ""

			nlInput := ""
			if len(args) > 0 {
				nlInput = args[0]
			}
			if nlInput == "" && date == "" && timeRange == "" {
				nlInput = p.question(`请输入会议安排 (如: "tom 2pm 1h shikai 团队周会" / "明天下午3点" / "6pm 30m"): `)
			}

			if nlInput != "" {
				parser := a.newNLPParser()
				parsed, err := parser.ParseOneShotWithDefaults(ctx, nlInput)
				if err != nil {
					a.log.Warn(fmt.Sprintf("自然语言解析失败，使用交互式输入兜底: %v", err))
				} else {
					a.log.Info(fmt.Sprintf("自然语言解析成功: date=%s, startTime=%s, endTime=%s, duration=%dmin, title=%s, participants=[%s]",
						parsed.Date, parsed.StartTime, parsed.EndTime, parsed.Duration, parsed.Title,
						strings.Join(parsed.Participants, ", ")))
					dateToBook = parsed.Date
					startTime = parsed.StartTime
					endTime = parsed.EndTime
					title = parsed.Title
					if len(parsed.Participants) > 0 {
						participants = strings.Join(parsed.Participants, " ")
					}
				}
			}

			if dateToBook == "" || startTime == "" || endTime == "" {
				dateToBook = date
				if dateToBook == "" {
					dateToBook = p.question("请输入会议日期 (MM-DD or YYYY-MM-DD): ")
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
					return fmt.Errorf("无效的日期格式，请使用 MM-DD 或 YYYY-MM-DD 格式")
				}
				dateToBook = normalized

				timeInput := timeRange
				if timeInput == "" {
					timeInput = p.question("请输入会议时间范围 (HH:MM-HH:MM or HH:MM:SS-HH:MM:SS): ")
					if timeInput == "" {
						return fmt.Errorf("时间不能为空")
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
					return fmt.Errorf("无效的时间格式，请使用 HH:MM-HH:MM 或 HH:MM:SS-HH:MM:SS 格式")
				}
			}

			// 过去时间检测：若开始时间早于当前时间，交互式让用户选择推迟或取消
			explicitArgs := nlInput == "" && date != "" && timeRange != ""
			if !explicitArgs && nlp.IsPastMeetingTime(dateToBook, startTime, a.now().In(a.loc), a.loc) {
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

			if participants == "" && !yes {
				participants = p.question("请输入参与者列表（邮箱前缀或群ID），用空格分隔 (可选，直接回车跳过): ")
			}

			if title == "" {
				if yes {
					title = defaultMeetingTitle
				} else {
					title = p.question(fmt.Sprintf("请输入会议标题(默认为 %q): ", defaultMeetingTitle))
					if title == "" {
						title = defaultMeetingTitle
					}
				}
			}

			service, err := a.newBookingService(ctx, !a.run)
			if err != nil {
				return err
			}

			participantList := strings.Fields(participants)
			taskOwner := env("TASK_OWNER")
			if taskOwner != "" && !containsString(participantList, taskOwner) {
				participantList = append(participantList, taskOwner)
			}

			_, err = service.BookRoom(ctx, dateToBook, startTime, endTime, title, participantList)
			return err
		},
	}

	cmd.Flags().StringVarP(&date, "date", "d", "", "会议日期 (MM-DD or YYYY-MM-DD)")
	cmd.Flags().StringVarP(&timeRange, "time", "t", "", "会议时间范围 (HH:MM-HH:MM or HH:MM:SS-HH:MM:SS)")
	cmd.Flags().StringVarP(&participants, "participants", "p", "", "参与者列表，用空格分隔")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "跳过参会人、标题等可选交互，使用默认值")
	return cmd
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

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

var _ = context.Background
