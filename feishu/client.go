package feishu

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkcalendar "github.com/larksuite/oapi-sdk-go/v3/service/calendar/v4"
	larkcontact "github.com/larksuite/oapi-sdk-go/v3/service/contact/v3"
	larkvc "github.com/larksuite/oapi-sdk-go/v3/service/vc/v1"
)

const cacheTTL = 30 * time.Minute

func ptr[T any](v T) *T { return &v }

// Config API 客户端配置。
type Config struct {
	AppID         string
	AppSecret     string
	AuthMode      AuthMode
	UserTokenPath string
	Debug         bool
}

// NewHTTPClient 构建共享的 HTTP 客户端。
func NewHTTPClient() *http.Client {
	return &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone(), Timeout: 30 * time.Second}
}

// API 飞书开放平台客户端：SDK 封装 + 身份决策 + 30 分钟内存缓存。
type API struct {
	lark  *lark.Client
	auth  *Auth
	log   *slog.Logger
	clock func() time.Time
	loc   *time.Location

	cachedCalendarID   string
	calendarIDExpiry   time.Time
	reserveConfigCache map[string]reserveConfigEntry
}

type reserveConfigEntry struct {
	config *ReserveConfig
	expiry time.Time
}

// slogAdapter 把 SDK 日志转发到 slog（stderr），避免污染 stdout。
type slogAdapter struct {
	log *slog.Logger
}

func (s *slogAdapter) Debug(_ context.Context, args ...any) { s.log.Debug(fmt.Sprint(args...)) }
func (s *slogAdapter) Info(_ context.Context, args ...any)  { s.log.Debug(fmt.Sprint(args...)) }
func (s *slogAdapter) Warn(_ context.Context, args ...any)  { s.log.Warn(fmt.Sprint(args...)) }
func (s *slogAdapter) Error(_ context.Context, args ...any) { s.log.Error(fmt.Sprint(args...)) }

// NewAPI 装配 API 客户端；httpClient 同时用于 SDK 与 OAuth 端点。
func NewAPI(cfg Config, httpClient *http.Client, log *slog.Logger, clock func() time.Time, loc *time.Location) *API {
	opts := []lark.ClientOptionFunc{
		lark.WithHttpClient(httpClient),
		lark.WithLogger(&slogAdapter{log: log}),
	}
	if cfg.Debug {
		opts = append(opts, lark.WithLogLevel(larkcore.LogLevelDebug), lark.WithLogReqAtDebug(true))
	}
	client := lark.NewClient(cfg.AppID, cfg.AppSecret, opts...)

	mode := cfg.AuthMode
	if mode == "" {
		mode = AuthModeAuto
	}
	auth := &Auth{
		Mode:        mode,
		TokenClient: &OAuthClient{HTTP: httpClient, AppID: cfg.AppID, AppSecret: cfg.AppSecret},
		Store:       &FileUserTokenStore{Path: cfg.UserTokenPath},
		Clock:       clock,
		Log:         log,
	}

	return &API{
		lark:               client,
		auth:               auth,
		log:                log,
		clock:              clock,
		loc:                loc,
		reserveConfigCache: map[string]reserveConfigEntry{},
	}
}

// Auth 暴露鉴权管理器（login 命令编排使用）。
func (a *API) Auth() *Auth { return a.auth }

// CurrentUser 当前授权用户身份；无用户凭证或身份不可得时返回 nil（应用身份预订）。
func (a *API) CurrentUser(ctx context.Context) *UserIdentity { return a.auth.UserIdentity(ctx) }

// VerifyCredentials 校验应用凭据（tenant_access_token 可获取）。
func (a *API) VerifyCredentials(ctx context.Context) error {
	return a.auth.TokenClient.VerifyTenantCredentials(ctx)
}

func (a *API) SearchRooms(ctx context.Context) ([]Room, error) {
	req := larkvc.NewSearchRoomReqBuilder().
		Body(larkvc.NewSearchRoomReqBodyBuilder().PageSize(100).Build()).
		Build()
	resp, err := a.lark.Vc.V1.Room.Search(ctx, req)
	if err != nil {
		return nil, err
	}
	if !resp.Success() {
		return nil, &APIError{Code: resp.Code, Msg: resp.Msg}
	}

	rooms := make([]Room, 0, len(resp.Data.Rooms))
	for _, r := range resp.Data.Rooms {
		status := 0
		if r.RoomStatus != nil && r.RoomStatus.Status != nil && *r.RoomStatus.Status {
			status = 1
		}
		rooms = append(rooms, Room{
			ID:           deref(r.RoomId),
			Name:         deref(r.Name),
			Capacity:     derefInt(r.Capacity),
			Description:  deref(r.Description),
			Status:       status,
			CustomRoomID: deref(r.CustomRoomId),
		})
	}
	a.log.Debug(fmt.Sprintf("查询到 %d 个会议室", len(rooms)))
	return rooms, nil
}

// GetRoomLevelChildren 查询 parentID 的子层级;parentID 为空时返回租户根层级列表
// (空字符串参数会被飞书以 param error 拒绝,必须整个省略)。
func (a *API) GetRoomLevelChildren(ctx context.Context, parentID string) ([]RoomLevel, error) {
	builder := larkvc.NewListRoomLevelReqBuilder().PageSize(100)
	if parentID != "" {
		builder = builder.RoomLevelId(parentID)
	}
	req := builder.Build()
	resp, err := a.lark.Vc.V1.RoomLevel.List(ctx, req)
	if err != nil {
		return nil, err
	}
	if !resp.Success() {
		return nil, &APIError{Code: resp.Code, Msg: resp.Msg}
	}

	levels := make([]RoomLevel, 0, len(resp.Data.Items))
	for _, l := range resp.Data.Items {
		levels = append(levels, RoomLevel{
			RoomLevelID: deref(l.RoomLevelId),
			Name:        deref(l.Name),
			HasChild:    derefBool(l.HasChild),
			ParentID:    deref(l.ParentId),
		})
	}
	a.log.Debug(fmt.Sprintf("查询到 %d 个会议室层级", len(levels)))
	return levels, nil
}

func (a *API) GetRoomsByLevel(ctx context.Context, levelID string) ([]RoomDetail, error) {
	req := larkvc.NewListRoomReqBuilder().RoomLevelId(levelID).PageSize(100).Build()
	resp, err := a.lark.Vc.V1.Room.List(ctx, req)
	if err != nil {
		return nil, err
	}
	if !resp.Success() {
		return nil, &APIError{Code: resp.Code, Msg: resp.Msg}
	}

	if derefBool(resp.Data.HasMore) {
		a.log.Warn(fmt.Sprintf("会议室数据不完整：还有更多会议室未获取，当前仅获取了 %d 个", len(resp.Data.Rooms)))
	}

	rooms := make([]RoomDetail, 0, len(resp.Data.Rooms))
	for _, r := range resp.Data.Rooms {
		detail := RoomDetail{
			ID:           deref(r.RoomId),
			Name:         deref(r.Name),
			Capacity:     derefInt(r.Capacity),
			Description:  deref(r.Description),
			CustomRoomID: deref(r.CustomRoomId),
			RoomLevelID:  deref(r.RoomLevelId),
			RoomStatus:   RoomStatus{DisableStartTime: "0", DisableEndTime: "0"},
		}
		if rs := r.RoomStatus; rs != nil {
			detail.RoomStatus.Status = derefBool(rs.Status)
			if rs.DisableStartTime != nil {
				detail.RoomStatus.DisableStartTime = *rs.DisableStartTime
			}
			if rs.DisableEndTime != nil {
				detail.RoomStatus.DisableEndTime = *rs.DisableEndTime
			}
		}
		rooms = append(rooms, detail)
	}
	a.log.Debug(fmt.Sprintf("在层级 %s 查询到 %d 个会议室", levelID, len(rooms)))
	return rooms, nil
}

func (a *API) GetRoomReserveConfig(ctx context.Context, roomID string) (*ReserveConfig, error) {
	now := a.clock()
	if cached, ok := a.reserveConfigCache[roomID]; ok && cached.expiry.After(now) {
		a.log.Debug(fmt.Sprintf("使用缓存的会议室预定策略: %s", roomID))
		return cached.config, nil
	}

	req := larkvc.NewReserveScopeReserveConfigReqBuilder().ScopeId(roomID).ScopeType("2").Build()
	resp, err := a.lark.Vc.V1.ReserveConfig.ReserveScope(ctx, req)
	if err != nil {
		return nil, err
	}
	if !resp.Success() || resp.Data.TimeConfig == nil {
		a.log.Warn(fmt.Sprintf("获取会议室预定策略失败: %d - %s", resp.Code, resp.Msg))
		return nil, nil
	}

	tc := resp.Data.TimeConfig
	config := &ReserveConfig{
		TimeSwitch:    derefInt(tc.TimeSwitch),
		DaysInAdvance: tc.DaysInAdvance,
		StartTime:     deref(tc.StartTime),
		EndTime:       deref(tc.EndTime),
		MaxDuration:   tc.MaxDuration,
	}
	a.reserveConfigCache[roomID] = reserveConfigEntry{config: config, expiry: now.Add(cacheTTL)}
	return config, nil
}

func (a *API) GetPrimaryCalendar(ctx context.Context) (string, error) {
	now := a.clock()
	if a.cachedCalendarID != "" && a.calendarIDExpiry.After(now) {
		a.log.Debug("使用缓存的主日历ID")
		return a.cachedCalendarID, nil
	}

	authOpts, err := a.auth.CalendarAuthOptions(ctx)
	if err != nil {
		return "", err
	}
	resp, err := a.lark.Calendar.V4.Calendar.Primary(ctx, larkcalendar.NewPrimaryCalendarReqBuilder().Build(), authOpts...)
	if err != nil {
		return "", err
	}
	if !resp.Success() {
		return "", &APIError{Code: resp.Code, Msg: fmt.Sprintf("获取主日历失败: %s", resp.Msg)}
	}
	if len(resp.Data.Calendars) == 0 || resp.Data.Calendars[0].Calendar == nil ||
		resp.Data.Calendars[0].Calendar.CalendarId == nil {
		return "", fmt.Errorf("获取主日历失败: 响应中缺少 calendar_id")
	}

	a.cachedCalendarID = *resp.Data.Calendars[0].Calendar.CalendarId
	a.calendarIDExpiry = now.Add(cacheTTL)
	a.log.Info(fmt.Sprintf("成功获取主日历ID: %s", a.cachedCalendarID))
	return a.cachedCalendarID, nil
}

func (a *API) CreateEvent(ctx context.Context, event Event) (string, error) {
	calendarID, err := a.GetPrimaryCalendar(ctx)
	if err != nil {
		return "", err
	}

	timezone := event.Timezone
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	reminderMinutes := event.ReminderMinutes
	if reminderMinutes == 0 {
		reminderMinutes = 15
	}
	body := &larkcalendar.CalendarEvent{
		Summary:     ptr(event.Title),
		Description: ptr(event.Description),
		StartTime: &larkcalendar.TimeInfo{
			Timestamp: ptr(strconv.FormatInt(event.StartTime, 10)),
			Timezone:  ptr(timezone),
		},
		EndTime: &larkcalendar.TimeInfo{
			Timestamp: ptr(strconv.FormatInt(event.EndTime, 10)),
			Timezone:  ptr(timezone),
		},
		Location:        &larkcalendar.EventLocation{Name: ptr(event.Location)},
		AttendeeAbility: ptr("can_see_others"),
		FreeBusyStatus:  ptr("busy"),
		Reminders:       []*larkcalendar.Reminder{{Minutes: ptr(reminderMinutes)}},
	}
	if event.Recurrence != "" {
		body.Recurrence = ptr(event.Recurrence)
	}

	authOpts, err := a.auth.CalendarAuthOptions(ctx)
	if err != nil {
		return "", err
	}
	req := larkcalendar.NewCreateCalendarEventReqBuilder().CalendarId(calendarID).CalendarEvent(body).Build()
	resp, err := a.lark.Calendar.V4.CalendarEvent.Create(ctx, req, authOpts...)
	if err != nil {
		return "", err
	}
	if !resp.Success() {
		return "", &APIError{Code: resp.Code, Msg: fmt.Sprintf("创建日程失败: %s", resp.Msg)}
	}

	eventID := ""
	if resp.Data.Event != nil {
		eventID = deref(resp.Data.Event.EventId)
	}
	a.log.Info(fmt.Sprintf("成功创建日程: %s", eventID))
	return eventID, nil
}

// AddAttendeesToEvent 逐个添加参与人（对齐 Node 版行为：单个失败仅告警不中断）。
func (a *API) AddAttendeesToEvent(ctx context.Context, eventID string, attendees []Attendee) error {
	calendarID, err := a.GetPrimaryCalendar(ctx)
	if err != nil {
		return err
	}
	authOpts, err := a.auth.CalendarAuthOptions(ctx)
	if err != nil {
		return err
	}

	for _, attendee := range attendees {
		sdkAttendee := &larkcalendar.CalendarEventAttendee{Type: ptr(attendee.Type)}
		switch attendee.Type {
		case "resource":
			sdkAttendee.RoomId = ptr(attendee.RoomID)
		case "chat":
			sdkAttendee.ChatId = ptr(attendee.ChatID)
		default:
			sdkAttendee.UserId = ptr(attendee.UserID)
		}

		req := larkcalendar.NewCreateCalendarEventAttendeeReqBuilder().
			CalendarId(calendarID).
			EventId(eventID).
			UserIdType("user_id").
			Body(larkcalendar.NewCreateCalendarEventAttendeeReqBodyBuilder().
				Attendees([]*larkcalendar.CalendarEventAttendee{sdkAttendee}).
				Build()).
			Build()
		resp, err := a.lark.Calendar.V4.CalendarEventAttendee.Create(ctx, req, authOpts...)
		if err != nil {
			return err
		}
		if !resp.Success() {
			a.log.Warn(fmt.Sprintf("添加参与者失败: %d - %s", resp.Code, resp.Msg))
		}
	}

	a.log.Info(fmt.Sprintf("成功为日程 %s 添加 %d 个参与者", eventID, len(attendees)))
	return nil
}

func (a *API) FindUsersByEmails(ctx context.Context, emails []string) ([]User, error) {
	var users []User

	const batchSize = 50
	for i := 0; i < len(emails); i += batchSize {
		batch := emails[i:min(i+batchSize, len(emails))]

		idReq := larkcontact.NewBatchGetIdUserReqBuilder().
			UserIdType("user_id").
			Body(larkcontact.NewBatchGetIdUserReqBodyBuilder().Emails(batch).Build()).
			Build()
		idResp, err := a.lark.Contact.V3.User.BatchGetId(ctx, idReq)
		if err != nil {
			return nil, err
		}
		if !idResp.Success() {
			a.log.Error(fmt.Sprintf("查询邮箱批次失败: %d - %s", idResp.Code, idResp.Msg))
			continue
		}

		var userIDs []string
		for _, item := range idResp.Data.UserList {
			if item.UserId != nil && *item.UserId != "" {
				userIDs = append(userIDs, *item.UserId)
			}
		}
		if len(userIDs) == 0 {
			continue
		}

		userReq := larkcontact.NewBatchUserReqBuilder().UserIds(userIDs).UserIdType("user_id").Build()
		userResp, err := a.lark.Contact.V3.User.Batch(ctx, userReq)
		if err != nil {
			return nil, err
		}
		if !userResp.Success() {
			continue
		}
		for _, u := range userResp.Data.Items {
			users = append(users, User{
				ID:    deref(u.UserId),
				Name:  deref(u.Name),
				Email: deref(u.EnterpriseEmail),
			})
		}
	}

	a.log.Debug(fmt.Sprintf("通过邮箱查询到 %d 个用户", len(users)))
	return users, nil
}

// GetRoomFreeBusy 查询会议室在时间段内是否空闲；查询失败时保守返回不可用。
func (a *API) GetRoomFreeBusy(ctx context.Context, roomID string, start, end time.Time) (bool, error) {
	req := larkcalendar.NewListFreebusyReqBuilder().
		Body(larkcalendar.NewListFreebusyReqBodyBuilder().
			TimeMin(start.In(a.loc).Format(time.RFC3339)).
			TimeMax(end.In(a.loc).Format(time.RFC3339)).
			RoomId(roomID).
			OnlyBusy(true).
			Build()).
		Build()
	resp, err := a.lark.Calendar.V4.Freebusy.List(ctx, req)
	if err != nil {
		return false, err
	}
	if !resp.Success() {
		a.log.Warn(fmt.Sprintf("查询会议室忙闲状态失败: %d - %s", resp.Code, resp.Msg))
		return false, nil
	}

	available := len(resp.Data.FreebusyList) == 0
	a.log.Debug(fmt.Sprintf("会议室 %s 在该时段%s", roomID, map[bool]string{true: "可用", false: "不可用"}[available]))
	return available, nil
}

func (a *API) GetCalendarEvents(ctx context.Context, start, end time.Time) ([]CalendarEvent, error) {
	calendarID, err := a.GetPrimaryCalendar(ctx)
	if err != nil {
		return nil, err
	}
	authOpts, err := a.auth.CalendarAuthOptions(ctx)
	if err != nil {
		return nil, err
	}

	const pageSize = 100
	req := larkcalendar.NewListCalendarEventReqBuilder().
		CalendarId(calendarID).
		StartTime(strconv.FormatInt(start.Unix(), 10)).
		EndTime(strconv.FormatInt(end.Unix(), 10)).
		PageSize(pageSize).
		Build()
	resp, err := a.lark.Calendar.V4.CalendarEvent.List(ctx, req, authOpts...)
	if err != nil {
		return nil, err
	}
	if !resp.Success() {
		return nil, &APIError{Code: resp.Code, Msg: fmt.Sprintf("获取日历事件失败: %s", resp.Msg)}
	}

	events := make([]CalendarEvent, 0, len(resp.Data.Items))
	for _, item := range resp.Data.Items {
		event := CalendarEvent{
			EventID:             deref(item.EventId),
			Summary:             deref(item.Summary),
			Description:         deref(item.Description),
			Status:              deref(item.Status),
			OrganizerCalendarID: deref(item.OrganizerCalendarId),
		}
		if item.StartTime != nil && item.StartTime.Timestamp != nil {
			event.StartTimestamp, _ = strconv.ParseInt(*item.StartTime.Timestamp, 10, 64)
		}
		if item.EndTime != nil && item.EndTime.Timestamp != nil {
			event.EndTimestamp, _ = strconv.ParseInt(*item.EndTime.Timestamp, 10, 64)
		}
		if item.Location != nil {
			event.LocationName = deref(item.Location.Name)
		}
		events = append(events, event)
	}

	a.log.Debug(fmt.Sprintf("获取到 %d 个日历事件", len(events)))
	if len(events) >= pageSize {
		a.log.Warn(fmt.Sprintf("获取的日历事件数据可能不完整，达到分页限制 %d 条，实际可能有更多数据", pageSize))
	}
	return events, nil
}

func (a *API) DeleteCalendarEvent(ctx context.Context, eventID string) error {
	calendarID, err := a.GetPrimaryCalendar(ctx)
	if err != nil {
		return err
	}
	authOpts, err := a.auth.CalendarAuthOptions(ctx)
	if err != nil {
		return err
	}

	req := larkcalendar.NewDeleteCalendarEventReqBuilder().CalendarId(calendarID).EventId(eventID).Build()
	resp, err := a.lark.Calendar.V4.CalendarEvent.Delete(ctx, req, authOpts...)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return &APIError{Code: resp.Code, Msg: fmt.Sprintf("删除日历事件失败: %s", resp.Msg)}
	}
	a.log.Info(fmt.Sprintf("成功删除日历事件: %s", eventID))
	return nil
}

// GetEventAttendees 获取日程参与人列表（用于确认会议室预约状态）。
func (a *API) GetEventAttendees(ctx context.Context, eventID string) ([]Attendee, error) {
	calendarID, err := a.GetPrimaryCalendar(ctx)
	if err != nil {
		return nil, err
	}
	authOpts, err := a.auth.CalendarAuthOptions(ctx)
	if err != nil {
		return nil, err
	}

	req := larkcalendar.NewGetCalendarEventReqBuilder().
		CalendarId(calendarID).
		EventId(eventID).
		NeedAttendee(true).
		Build()
	resp, err := a.lark.Calendar.V4.CalendarEvent.Get(ctx, req, authOpts...)
	if err != nil {
		return nil, err
	}
	if !resp.Success() {
		return nil, &APIError{Code: resp.Code, Msg: fmt.Sprintf("获取日历事件详情失败: %s", resp.Msg)}
	}

	var attendees []Attendee
	if resp.Data.Event != nil {
		for _, at := range resp.Data.Event.Attendees {
			attendees = append(attendees, Attendee{
				Type:       deref(at.Type),
				UserID:     deref(at.UserId),
				RoomID:     deref(at.RoomId),
				ChatID:     deref(at.ChatId),
				RsvpStatus: deref(at.RsvpStatus),
			})
		}
	}
	return attendees, nil
}

func (a *API) hasRoomAttendee(ctx context.Context, eventID, roomID string) bool {
	attendees, err := a.GetEventAttendees(ctx, eventID)
	if err != nil {
		a.log.Error(fmt.Sprintf("检查会议室 %s 是否在事件 %s 中失败: %v", roomID, eventID, err))
		return false
	}
	for _, at := range attendees {
		if at.Type == "resource" && at.RoomID == roomID &&
			at.RsvpStatus != "decline" && at.RsvpStatus != "removed" {
			return true
		}
	}
	return false
}

// BookRoomWithEvent 创建日程并预约会议室：先加会议室资源，指数退避轮询确认
// 会议室接受后再加用户/群参与者；确认失败则删除日程回滚。
func (a *API) BookRoomWithEvent(ctx context.Context, event Event, roomID string, userIDs []string) (string, error) {
	const (
		maxRetries       = 4
		initDelaySeconds = 1
	)

	eventID, err := a.CreateEvent(ctx, event)
	if err != nil {
		return "", err
	}
	a.log.Info(fmt.Sprintf("创建会议事件成功: %s, 开始异步预约会议室 %s", eventID, roomID))

	cleanup := func() {
		if err := a.DeleteCalendarEvent(ctx, eventID); err != nil {
			a.log.Error(fmt.Sprintf("清理失败事件 %s 时发生错误: %v", eventID, err))
		}
	}

	if err := a.AddAttendeesToEvent(ctx, eventID, []Attendee{{Type: "resource", RoomID: roomID}}); err != nil {
		cleanup()
		return "", err
	}

	confirmed := false
	for retry := 0; retry < maxRetries; retry++ {
		delay := time.Duration(initDelaySeconds<<retry) * time.Second
		time.Sleep(delay)

		if a.hasRoomAttendee(ctx, eventID, roomID) {
			a.log.Info(fmt.Sprintf("会议室 %s 预约成功，第 %d 次检查", roomID, retry+1))
			confirmed = true
			break
		}
		a.log.Warn(fmt.Sprintf("会议室 %s 预约未确认，第 %d 次重试", roomID, retry+1))
	}

	if !confirmed {
		a.log.Error(fmt.Sprintf("会议室 %s 预约失败，经过 %d 次重试仍未成功，取消会议 %s", roomID, maxRetries, eventID))
		cleanup()
		return "", fmt.Errorf("会议室 %s 预约失败，会议已取消", roomID)
	}

	if len(userIDs) > 0 {
		attendees := make([]Attendee, 0, len(userIDs))
		for _, id := range userIDs {
			if len(id) > 3 && id[:3] == "oc_" {
				attendees = append(attendees, Attendee{Type: "chat", ChatID: id})
			} else {
				attendees = append(attendees, Attendee{Type: "user", UserID: id})
			}
		}
		if err := a.AddAttendeesToEvent(ctx, eventID, attendees); err != nil {
			cleanup()
			return "", err
		}
	}

	a.log.Info(fmt.Sprintf("异步会议室预约流程完成: 事件 %s, 会议室 %s", eventID, roomID))
	return eventID, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt(i *int) int {
	if i == nil {
		return 0
	}
	return *i
}

func derefBool(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}
