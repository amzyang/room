package main

import (
	"context"
	"fmt"
	"os"

	"github.com/amzyang/room/booking"
	"github.com/amzyang/room/config"
	"github.com/amzyang/room/envutil"
	"github.com/amzyang/room/feishu"
	"github.com/amzyang/room/nlp"
	"github.com/amzyang/room/output"
)

const (
	defaultCancelDays    = 31
	defaultMeetingTitle  = "meeting"
	defaultUserTokenPath = config.DefaultUserTokenPath
	defaultUserAuthScope = config.DefaultUserAuthScope
	holidayCacheDir      = ".cache/holidays"
	userIDCachePath      = ".cache/user_ids_lark.json"
	autoBookingCachePath = ".cache/auto-booking-cache.json"
)

func env(key string) string {
	return envutil.CleanEnvValue(os.Getenv(key))
}

// bookingService auto/book/list/cancel 命令依赖的服务能力（测试注入 fake）。
type bookingService interface {
	ListMyEvents(ctx context.Context, days int, organizedByMeOnly bool) ([]booking.EventSummary, error)
	CancelEvent(ctx context.Context, eventID string) (*booking.CancelOutcome, error)
	BookRoom(ctx context.Context, date, startTime, endTime, title string, participants []string) (*booking.BookResult, error)
	AutoBook(ctx context.Context, dryRun bool) ([]booking.BookResult, error)
}

// newBookingService 组装预订服务（唯一的配置校验层），并完成初始化。
func (a *app) newBookingService(ctx context.Context, dryRun bool) (*booking.Service, error) {
	appID := env("FEISHU_APP_ID")
	appSecret := env("FEISHU_APP_SECRET")
	if appID == "" || appSecret == "" {
		return nil, output.Errf(output.TypeConfig,
			"运行 room init 自动创建应用，或 room config set feishu.app_id / feishu.app_secret 手动写入",
			"缺失飞书应用凭证（FEISHU_APP_ID / FEISHU_APP_SECRET）")
	}
	taskOwner := env("TASK_OWNER")
	if taskOwner == "" {
		return nil, output.Errf(output.TypeConfig,
			"运行 room config set booking.task_owner <邮箱前缀> 设置", "缺失 TASK_OWNER 配置")
	}
	roomList := envutil.ParseEnvList(os.Getenv("ROOM_LIST"))
	if len(roomList) == 0 {
		return nil, output.Errf(output.TypeConfig,
			"运行 room config set booking.room_list <逗号分隔的会议室名> 设置", "缺失 ROOM_LIST 配置")
	}

	userTokenPath := env("FEISHU_USER_TOKEN_PATH")
	if userTokenPath == "" {
		userTokenPath = defaultUserTokenPath
	}

	httpClient := feishu.NewHTTPClient()
	api := feishu.NewAPI(feishu.Config{
		AppID:         appID,
		AppSecret:     appSecret,
		AuthMode:      feishu.AuthMode(env("FEISHU_AUTH_MODE")),
		UserTokenPath: userTokenPath,
		Debug:         a.debug,
	}, httpClient, a.log, a.now, a.loc)

	service := &booking.Service{
		API: api,
		Cfg: booking.Config{
			RoomList:        roomList,
			RoomExcludeList: envutil.ParseEnvList(os.Getenv("ROOM_EXCLUDE_LIST")),
			RoomLevelID:     env("ROOM_LEVEL_ID"),
			RoomSize:        envutil.ParseEnvInt(os.Getenv("ROOM_SIZE"), 0),
			TaskFormat:      env("TASK_FORMAT"),
			TaskOwner:       taskOwner,
			EmailDomain:     env("EMAIL_DOMAIN"),
			TianAPIKey:      env("TIANAPI_KEY"),
		},
		Log:          a.log,
		Clock:        a.now,
		Loc:          a.loc,
		HTTP:         httpClient,
		HolidayCache: &booking.HolidayCache{Dir: holidayCacheDir},
		UserIDs:      &booking.UserIDCache{Path: userIDCachePath},
		AutoCache:    &booking.AutoBookingCache{Path: autoBookingCachePath},
	}

	mode := "执行模式"
	if dryRun {
		mode = "模拟模式"
	}
	a.log.Info(fmt.Sprintf("启动飞书预订服务: %s", mode))

	if err := service.Initialize(ctx); err != nil {
		return nil, output.Wrap(output.TypeAuth, "检查凭证是否有效，必要时重新运行 room init 与 room login", err)
	}
	return service, nil
}

// newNLPParser 组装自然语言解析器。
func (a *app) newNLPParser() *nlp.Parser {
	base := env("OPENAI_API_BASE")
	if base == "" {
		base = nlp.DefaultAPIBase
	}
	model := env("MODEL")
	if model == "" {
		model = nlp.DefaultModel
	}
	return &nlp.Parser{
		APIKey:  env("OPENAI_API_KEY"),
		BaseURL: base,
		Model:   model,
		HTTP:    feishu.NewHTTPClient(),
		Log:     a.log,
		Clock:   a.now,
		Loc:     a.loc,
	}
}
