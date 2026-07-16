package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/amzyang/room/booking"
	"github.com/amzyang/room/config"
	"github.com/amzyang/room/envutil"
	"github.com/amzyang/room/feishu"
	"github.com/amzyang/room/nlp"
	"github.com/amzyang/room/output"
)

const (
	defaultCancelDays   = 31
	defaultMeetingTitle = "meeting"

	// userAuthScope login 固定申请的用户授权 scope,按本工具实际调用的 API 划定
	// (offline_access 由 tokenclient 自动补)。
	userAuthScope = "calendar:calendar calendar:calendar:readonly calendar:calendar:read calendar:calendar.event:create calendar:calendar.event:read calendar:calendar.event:delete calendar:calendar.free_busy:read vc:room:readonly vc:reserve:readonly contact:user.base:readonly contact:user.employee_id:readonly contact:contact.base:readonly"
)

func env(key string) string {
	return envutil.CleanEnvValue(os.Getenv(key))
}

// userTokenPath 用户凭证文件路径,固定在缓存目录下,不再可配。
func userTokenPath() string {
	return filepath.Join(config.CacheDir(), "feishu-user-token.json")
}

// newFeishuAPI 装配飞书 API 客户端(调用方先确保凭证已配置)。
func (a *app) newFeishuAPI(httpClient *http.Client) *feishu.API {
	return feishu.NewAPI(feishu.Config{
		AppID:         env("FEISHU_APP_ID"),
		AppSecret:     env("FEISHU_APP_SECRET"),
		AuthMode:      feishu.AuthMode(env("FEISHU_AUTH_MODE")),
		UserTokenPath: userTokenPath(),
		Debug:         a.debug,
	}, httpClient, a.log, a.now, a.loc)
}

// bookingService auto/book/list/cancel 命令依赖的服务能力（测试注入 fake）。
type bookingService interface {
	ListEvents(ctx context.Context, from, to time.Time, organizedByMeOnly bool) ([]booking.EventSummary, error)
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
	roomList := envutil.ParseEnvList(os.Getenv("ROOM_LIST"))
	if len(roomList) == 0 {
		return nil, output.Errf(output.TypeConfig,
			"运行 room config set booking.room_list <逗号分隔的会议室名> 设置", "缺失 ROOM_LIST 配置")
	}

	httpClient := feishu.NewHTTPClient()
	api := a.newFeishuAPI(httpClient)

	service := &booking.Service{
		API: api,
		Cfg: booking.Config{
			RoomList:        roomList,
			RoomExcludeList: envutil.ParseEnvList(os.Getenv("ROOM_EXCLUDE_LIST")),
			RoomLevelID:     env("ROOM_LEVEL_ID"),
			RoomSize:        envutil.ParseEnvInt(os.Getenv("ROOM_SIZE"), 0),
			TaskFormat:      env("TASK_FORMAT"),
			EmailDomain:     env("EMAIL_DOMAIN"),
			TianAPIKey:      env("TIANAPI_KEY"),
		},
		Log:          a.log,
		Clock:        a.now,
		Loc:          a.loc,
		HTTP:         httpClient,
		HolidayCache: &booking.HolidayCache{Dir: filepath.Join(config.CacheDir(), "holidays")},
		UserIDs:      &booking.UserIDCache{Path: filepath.Join(config.CacheDir(), "user_ids_lark.json")},
		AutoCache:    &booking.AutoBookingCache{Path: filepath.Join(config.CacheDir(), "auto-booking-cache.json")},
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
