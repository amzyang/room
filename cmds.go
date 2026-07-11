package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/amzyang/room/booking"
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
				return fmt.Errorf("缺失 FEISHU_BOT_WEBHOOK 配置，请在 .env 中设置自定义机器人 webhook 地址")
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
