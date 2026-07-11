package main

import (
	"context"
	"fmt"
	"os"

	"github.com/amzyang/room/booking"
	"github.com/amzyang/room/envutil"
	"github.com/amzyang/room/feishu"
	"github.com/amzyang/room/nlp"
)

const (
	defaultCancelDays       = 31
	defaultMeetingTitle     = "meeting"
	defaultUserTokenPath    = ".cache/feishu-user-token.json"
	defaultUserAuthScope    = "calendar:calendar calendar:calendar:readonly calendar:calendar:read calendar:calendar.event:create calendar:calendar.event:read calendar:calendar.event:delete calendar:calendar.free_busy:read vc:room:readonly vc:reserve:readonly contact:user.base:readonly contact:user.employee_id:readonly contact:contact.base:readonly"
	holidayCacheDir         = ".cache/holidays"
	userIDCachePath         = ".cache/user_ids_lark.json"
	autoBookingCachePath    = ".cache/auto-booking-cache.json"
)

func env(key string) string {
	return envutil.CleanEnvValue(os.Getenv(key))
}

// tlsInsecure ROOM_TLS_INSECURE 默认开启（对齐原 Node 版全局关闭 TLS 校验），设 0 关闭。
func tlsInsecure() bool {
	return env("ROOM_TLS_INSECURE") != "0"
}

// newBookingService 组装预订服务（唯一的配置校验层），并完成初始化。
func (a *app) newBookingService(ctx context.Context, dryRun bool) (*booking.Service, error) {
	appID := env("FEISHU_APP_ID")
	appSecret := env("FEISHU_APP_SECRET")
	if appID == "" || appSecret == "" {
		return nil, fmt.Errorf("飞书配置缺失，请检查 FEISHU_APP_ID 和 FEISHU_APP_SECRET 环境变量")
	}
	taskOwner := env("TASK_OWNER")
	if taskOwner == "" {
		return nil, fmt.Errorf("缺失 TASK_OWNER 配置，请检查 .env 文件")
	}
	roomList := envutil.ParseEnvList(os.Getenv("ROOM_LIST"))
	if len(roomList) == 0 {
		return nil, fmt.Errorf("缺失 ROOM_LIST 配置，请检查 .env 文件")
	}

	userTokenPath := env("FEISHU_USER_TOKEN_PATH")
	if userTokenPath == "" {
		userTokenPath = defaultUserTokenPath
	}

	httpClient := feishu.NewHTTPClient(tlsInsecure())
	api := feishu.NewAPI(feishu.Config{
		AppID:         appID,
		AppSecret:     appSecret,
		AuthMode:      feishu.AuthMode(env("FEISHU_AUTH_MODE")),
		UserTokenPath: userTokenPath,
		Debug:         a.debug,
		TLSInsecure:   tlsInsecure(),
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
		return nil, err
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
		HTTP:    feishu.NewHTTPClient(tlsInsecure()),
		Log:     a.log,
		Clock:   a.now,
		Loc:     a.loc,
	}
}
