package booking

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/amzyang/room/feishu"
)

const (
	defaultMaxBookingDays = 15
	chatGroupPrefix       = "oc_"
	tianAPIURL            = "https://apis.tianapi.com/jiejiari/index"
)

// API 服务层依赖的飞书能力抽象，测试注入 fake。
type API interface {
	VerifyCredentials(ctx context.Context) error
	SearchRooms(ctx context.Context) ([]feishu.Room, error)
	GetRoomLevelChildren(ctx context.Context, parentID string) ([]feishu.RoomLevel, error)
	GetRoomsByLevel(ctx context.Context, levelID string) ([]feishu.RoomDetail, error)
	GetRoomReserveConfig(ctx context.Context, roomID string) (*feishu.ReserveConfig, error)
	GetPrimaryCalendar(ctx context.Context) (string, error)
	GetCalendarEvents(ctx context.Context, start, end time.Time) ([]feishu.CalendarEvent, error)
	DeleteCalendarEvent(ctx context.Context, eventID string) error
	GetRoomFreeBusy(ctx context.Context, roomID string, start, end time.Time) (bool, error)
	FindUsersByEmails(ctx context.Context, emails []string) ([]feishu.User, error)
	BookRoomWithEvent(ctx context.Context, event feishu.Event, roomID string, userIDs []string) (string, error)
}

// Config 预订服务配置（由入口层从 env 组装并校验）。
type Config struct {
	RoomList        []string
	RoomExcludeList []string
	RoomLevelID     string
	RoomSize        int
	TaskFormat      string
	TaskOwner       string
	EmailDomain     string
	TianAPIKey      string
}

// Service 预订编排服务。
type Service struct {
	API   API
	Cfg   Config
	Log   *slog.Logger
	Clock func() time.Time
	Loc   *time.Location
	HTTP  *http.Client

	HolidayCache *HolidayCache
	UserIDs      *UserIDCache
	AutoCache    *AutoBookingCache

	holidays map[string]bool
}

// Initialize 加载节假日与自动预订缓存，并校验飞书凭据。
func (s *Service) Initialize(ctx context.Context) error {
	s.holidays = map[string]bool{}
	s.loadHolidays(ctx, s.Clock().In(s.Loc).Format("2006"))

	s.UserIDs.Load()
	s.AutoCache.Load()

	if err := s.API.VerifyCredentials(ctx); err != nil {
		return fmt.Errorf("飞书认证失败，请检查 App ID 和 App Secret: %w", err)
	}
	s.Log.Info("飞书服务初始化成功")
	return nil
}

func (s *Service) loadHolidays(ctx context.Context, year string) {
	if cached := s.HolidayCache.Get(year); cached != nil {
		s.Log.Info(fmt.Sprintf("使用缓存的节假日数据: %s", year))
		s.processHolidayData(cached)
		return
	}

	if s.Cfg.TianAPIKey == "" {
		s.Log.Warn("未配置 TIANAPI_KEY，跳过节假日数据加载（节假日将不会被过滤）")
		return
	}

	params := url.Values{"key": {s.Cfg.TianAPIKey}, "date": {year}, "type": {"1"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tianAPIURL+"?"+params.Encode(), nil)
	if err != nil {
		s.Log.Error(fmt.Sprintf("获取节假日数据失败: %v", err))
		return
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		s.Log.Error(fmt.Sprintf("获取节假日数据失败: %v", err))
		return
	}
	defer resp.Body.Close()

	var holidayData HolidayResponse
	if err := json.NewDecoder(resp.Body).Decode(&holidayData); err != nil {
		s.Log.Error(fmt.Sprintf("解析节假日数据失败: %v", err))
		return
	}
	if holidayData.Code == 200 {
		if err := s.HolidayCache.Save(year, &holidayData); err != nil {
			s.Log.Error(fmt.Sprintf("保存节假日缓存失败: %v", err))
		}
		s.processHolidayData(&holidayData)
	}
}

func (s *Service) processHolidayData(data *HolidayResponse) {
	if data.Code != 200 {
		return
	}
	for _, holiday := range data.Result.List {
		if holiday.Vacation == "" {
			continue
		}
		for _, date := range strings.Split(holiday.Vacation, "|") {
			s.holidays[date] = true
		}
	}
}

// ListMyEvents 列出未来 days 天内的日历事件（按开始时间升序，过滤已取消/已删除）。
// organizedByMeOnly 为 true 时仅保留本人组织的事件。
func (s *Service) ListMyEvents(ctx context.Context, days int, organizedByMeOnly bool) ([]EventSummary, error) {
	now := s.Clock()
	events, err := s.API.GetCalendarEvents(ctx, now, now.AddDate(0, 0, days))
	if err != nil {
		return nil, err
	}

	myCalendarID := ""
	if organizedByMeOnly {
		if myCalendarID, err = s.API.GetPrimaryCalendar(ctx); err != nil {
			return nil, err
		}
	}

	var formatted []EventSummary
	for _, event := range events {
		if event.Status == "cancelled" || event.Status == "deleted" {
			s.Log.Debug(fmt.Sprintf("过滤掉已取消/删除的事件: %s", event.Summary))
			continue
		}
		if organizedByMeOnly && !IsOrganizedBy(event.OrganizerCalendarID, myCalendarID) {
			s.Log.Debug(fmt.Sprintf("过滤掉非本人组织的事件: %s", event.Summary))
			continue
		}

		summary := EventSummary{
			EventID:        event.EventID,
			Title:          event.Summary,
			Description:    event.Description,
			Location:       event.LocationName,
			StartTimestamp: event.StartTimestamp,
			EndTimestamp:   event.EndTimestamp,
		}
		if summary.Title == "" {
			summary.Title = "无标题"
		}
		if event.StartTimestamp > 0 {
			summary.StartTime = time.Unix(event.StartTimestamp, 0).In(s.Loc).Format("2006-01-02 15:04:05")
		}
		if event.EndTimestamp > 0 {
			summary.EndTime = time.Unix(event.EndTimestamp, 0).In(s.Loc).Format("2006-01-02 15:04:05")
		}
		formatted = append(formatted, summary)
	}

	sort.SliceStable(formatted, func(i, j int) bool {
		return formatted[i].StartTimestamp < formatted[j].StartTimestamp
	})

	s.Log.Info(fmt.Sprintf("获取到 %d 个未来 %d 天的日历事件", len(formatted), days))
	return formatted, nil
}

// CancelEvent 取消日历事件；事件已被取消/删除（code 193003）视为幂等成功。
func (s *Service) CancelEvent(ctx context.Context, eventID string) (*CancelOutcome, error) {
	if err := s.API.DeleteCalendarEvent(ctx, eventID); err != nil {
		if feishu.IsEventDeleted(err) {
			s.Log.Info(fmt.Sprintf("事件 %s 已被取消或删除", eventID))
			return &CancelOutcome{AlreadyCancelled: true}, nil
		}
		return nil, err
	}
	s.Log.Info(fmt.Sprintf("成功取消日历事件: %s", eventID))
	return &CancelOutcome{}, nil
}

// AutoBook 按 TASK_FORMAT 在最大可预订天数窗口内批量预订，返回逐条结果。
// 批量语义：单条未订到/失败不是整体错误，靠结果的 Status 区分。
func (s *Service) AutoBook(ctx context.Context, dryRun bool) ([]BookResult, error) {
	tasks := ParseTaskFormat(s.Cfg.TaskFormat, s.Cfg.TaskOwner)
	maxDays := s.getMaxBookingDays(ctx)
	now := s.Clock().In(s.Loc)
	deadline := endOfDay(now.AddDate(0, 0, maxDays))

	var results []BookResult
	for _, task := range tasks {
		results = append(results, s.processTask(ctx, task, dryRun, now, deadline, maxDays)...)
	}
	return results, nil
}

func (s *Service) processTask(ctx context.Context, task Task, dryRun bool, now, deadline time.Time, maxDays int) []BookResult {
	var targetDates []time.Time
	for date := now; !dateAfter(date, deadline); date = date.AddDate(0, 0, 1) {
		if IsDayOfWeekMatch(date, task.DayOfWeek) && IsInCycle(date, task, s.Loc) {
			targetDates = append(targetDates, date)
		}
	}

	if len(targetDates) == 0 {
		s.Log.Warn(fmt.Sprintf("在%d天内未找到符合条件的预订日期: %s", maxDays, task.DayOfWeek))
		return nil
	}

	var results []BookResult
	for _, targetDate := range targetDates {
		dateStr := targetDate.Format("2006-01-02")
		logPrefix := ""
		if dryRun {
			logPrefix = "[模拟模式]"
		}
		s.Log.Info(fmt.Sprintf("%s 计划预订: %s %s-%s %s", logPrefix, dateStr, task.StartTime, task.EndTime, task.Title))

		planned := BookResult{Status: StatusPlanned, Date: dateStr, StartTime: task.StartTime, EndTime: task.EndTime, Title: task.Title}
		if dryRun {
			results = append(results, planned)
			continue
		}
		result, err := s.BookRoom(ctx, dateStr, task.StartTime, task.EndTime, task.Title, task.Participants)
		if err != nil {
			s.Log.Error(fmt.Sprintf("预订失败: %s %s: %v", dateStr, task.Title, err))
			planned.Status = StatusFailed
			results = append(results, planned)
			continue
		}
		results = append(results, *result)
	}
	return results
}

// BookRoom 预订单个时间段：节假日/重叠检查 → 找可用会议室 → 建日程并确认。
// 未预订（节假日、重叠、无可用会议室）通过 BookResult.Status 区分，不视为 error。
func (s *Service) BookRoom(ctx context.Context, date, startTime, endTime, title string, participants []string) (*BookResult, error) {
	dateStr := date
	result := &BookResult{Date: dateStr, StartTime: startTime, EndTime: endTime, Title: title}

	if s.holidays[dateStr] {
		s.Log.Warn(fmt.Sprintf("跳过节假日: %s", dateStr))
		result.Status = StatusHolidaySkipped
		return result, nil
	}

	startDateTime, err := time.ParseInLocation("2006-01-02 15:04:05", dateStr+" "+startTime, s.Loc)
	if err != nil {
		return nil, fmt.Errorf("无效的开始时间: %w", err)
	}
	endDateTime, err := time.ParseInLocation("2006-01-02 15:04:05", dateStr+" "+endTime, s.Loc)
	if err != nil {
		return nil, fmt.Errorf("无效的结束时间: %w", err)
	}

	dayStart := startOfDay(startDateTime)
	existingEvents, err := s.API.GetCalendarEvents(ctx, dayStart, endOfDay(startDateTime))
	if err != nil {
		return nil, err
	}

	if s.hasOverlapEvent(existingEvents, startDateTime, endDateTime, dateStr, startTime, endTime) {
		s.Log.Warn(fmt.Sprintf("跳过已有日历事件的时间段: %s %s-%s", dateStr, startTime, endTime))
		result.Status = StatusConflict
		return result, nil
	}

	room := s.findAvailableRoom(ctx, s.Cfg.RoomList, dateStr, startTime, endTime)
	if room == nil {
		s.Log.Warn(fmt.Sprintf("未找到可用会议室: %s", strings.Join(s.Cfg.RoomList, ", ")))
		result.Status = StatusNoRoom
		return result, nil
	}

	event := feishu.Event{
		Title:           title,
		Description:     "会议室: " + room.Name,
		StartTime:       startDateTime.Unix(),
		EndTime:         endDateTime.Unix(),
		Timezone:        "Asia/Shanghai",
		Location:        room.Name,
		ReminderMinutes: 15,
	}

	var participantIDs []string
	for _, participant := range participants {
		if id := s.resolveParticipantID(ctx, participant); id != "" {
			participantIDs = append(participantIDs, id)
		}
	}

	eventID, err := s.API.BookRoomWithEvent(ctx, event, room.ID, participantIDs)
	if err != nil {
		return nil, err
	}

	s.Log.Info(fmt.Sprintf("成功预订会议室: %s, 日程ID: %s", room.Name, eventID))
	s.AutoCache.Add(eventID)
	if err := s.AutoCache.Save(); err != nil {
		s.Log.Error(fmt.Sprintf("保存自动预订缓存失败: %v", err))
	}
	result.Status = StatusBooked
	result.EventID = eventID
	result.Room = &BookedRoom{ID: room.ID, Name: room.Name}
	result.ParticipantsResolved = len(participantIDs)
	return result, nil
}

func (s *Service) hasOverlapEvent(events []feishu.CalendarEvent, start, end time.Time, dateStr, startTime, endTime string) bool {
	for _, event := range events {
		if event.StartTimestamp == 0 || event.EndTimestamp == 0 {
			continue
		}
		eventStart := time.Unix(event.StartTimestamp, 0)
		eventEnd := time.Unix(event.EndTimestamp, 0)

		if event.Status == "cancelled" || event.Status == "deleted" {
			// 本工具创建后被取消的会议仍视为占用，避免在被人为取消的时段反复重订
			if event.EventID != "" && s.AutoCache.Has(event.EventID) &&
				HasTimeOverlap(start, end, eventStart, eventEnd) {
				s.Log.Info(fmt.Sprintf("发现已取消的自动预订会议，视为时间段占用: %s %s-%s eventId=%s",
					dateStr, startTime, endTime, event.EventID))
				return true
			}
			continue
		}

		if HasTimeOverlap(start, end, eventStart, eventEnd) {
			s.Log.Info(fmt.Sprintf("时间段已有日历事件重叠: %s %s-%s", dateStr, startTime, endTime))
			return true
		}
	}
	return false
}

func (s *Service) resolveParticipantID(ctx context.Context, participant string) string {
	if strings.HasPrefix(participant, chatGroupPrefix) {
		s.Log.Debug(fmt.Sprintf("识别为群组ID: %s", participant))
		return participant
	}

	if cached := s.UserIDs.Get(participant); cached != "" {
		s.Log.Debug(fmt.Sprintf("使用缓存的参与者ID: %s", participant))
		return cached
	}

	if s.Cfg.EmailDomain == "" {
		s.Log.Error(fmt.Sprintf("未配置 EMAIL_DOMAIN，无法解析参与者: %s", participant))
		return ""
	}

	email := participant + "@" + s.Cfg.EmailDomain
	users, err := s.API.FindUsersByEmails(ctx, []string{email})
	if err != nil {
		s.Log.Error(fmt.Sprintf("查找参与者失败: %s: %v", participant, err))
		return ""
	}
	if len(users) > 0 {
		user := users[0]
		if err := s.UserIDs.Set(participant, user.ID); err != nil {
			s.Log.Error(fmt.Sprintf("保存用户ID缓存失败: %v", err))
		}
		s.Log.Debug(fmt.Sprintf("通过邮箱前缀 %s 找到用户: %s (%s)", participant, user.Name, user.ID))
		return user.ID
	}

	s.Log.Warn(fmt.Sprintf("参与者未找到: %s (邮箱: %s)", participant, email))
	return ""
}

func (s *Service) findAvailableRoom(ctx context.Context, roomNames []string, date, startTime, endTime string) *feishu.Room {
	if s.Cfg.RoomLevelID != "" {
		return s.findAvailableRoomByLevel(ctx, roomNames, date, startTime, endTime)
	}

	rooms, err := s.API.SearchRooms(ctx)
	if err != nil {
		s.Log.Error(fmt.Sprintf("查找可用会议室失败: %v", err))
		return nil
	}

	// 预检：用第一个正常开放的房间的策略校验日期是否在可预订范围内
	for _, room := range rooms {
		if room.Status == 1 {
			if !s.checkDaysInAdvance(ctx, room.ID, date) {
				return nil
			}
			break
		}
	}

	start, end := s.bookingRange(date, startTime, endTime)

	// 优先精确匹配会议室名称
	for _, roomName := range roomNames {
		for i := range rooms {
			room := &rooms[i]
			if (room.Name != roomName && room.CustomRoomID != roomName) || room.Status != 1 {
				continue
			}
			if s.shouldExcludeRoom(room.Name) || s.isRoomCapacityExceeded(room.Capacity) {
				continue
			}
			if !s.checkRoomReservePolicy(ctx, room.ID, room.Name, date, startTime, endTime) {
				continue
			}
			if available, _ := s.API.GetRoomFreeBusy(ctx, room.ID, start, end); available {
				s.Log.Info(fmt.Sprintf("找到可用会议室: %s", room.Name))
				return room
			}
			s.Log.Debug(fmt.Sprintf("会议室 %s 在预订时间段内不可用，跳过", room.Name))
		}
	}

	// 部分匹配会议室名称
	for i := range rooms {
		room := &rooms[i]
		if room.Status != 1 || s.shouldExcludeRoom(room.Name) || s.isRoomCapacityExceeded(room.Capacity) {
			continue
		}
		matched := false
		for _, name := range roomNames {
			if strings.Contains(room.Name, name) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if !s.checkRoomReservePolicy(ctx, room.ID, room.Name, date, startTime, endTime) {
			continue
		}
		if available, _ := s.API.GetRoomFreeBusy(ctx, room.ID, start, end); available {
			s.Log.Info(fmt.Sprintf("找到可用会议室: %s", room.Name))
			return room
		}
		s.Log.Debug(fmt.Sprintf("会议室 %s 在预订时间段内不可用，跳过", room.Name))
	}

	return nil
}

func (s *Service) findAvailableRoomByLevel(ctx context.Context, roomNames []string, date, startTime, endTime string) *feishu.Room {
	childLevels, err := s.API.GetRoomLevelChildren(ctx, s.Cfg.RoomLevelID)
	if err != nil {
		s.Log.Error(fmt.Sprintf("基于层级查找可用会议室失败: %v", err))
		return nil
	}
	s.Log.Debug(fmt.Sprintf("找到 %d 个子层级", len(childLevels)))

	allRooms, err := s.API.GetRoomsByLevel(ctx, s.Cfg.RoomLevelID)
	if err != nil {
		s.Log.Error(fmt.Sprintf("基于层级查找可用会议室失败: %v", err))
		return nil
	}
	if len(allRooms) == 0 {
		s.Log.Debug(fmt.Sprintf("层级 %s 下没有会议室", s.Cfg.RoomLevelID))
		return nil
	}

	// 预检：用第一个正常开放的房间的策略校验日期是否在可预订范围内
	for _, room := range allRooms {
		if room.RoomStatus.Status {
			if !s.checkDaysInAdvance(ctx, room.ID, date) {
				return nil
			}
			break
		}
	}

	sortedRooms := sortRoomsByPriorityAndLevel(allRooms, roomNames, childLevels)
	start, end := s.bookingRange(date, startTime, endTime)

	for i := range sortedRooms {
		room := &sortedRooms[i]
		if s.shouldExcludeRoom(room.Name) {
			s.Log.Debug(fmt.Sprintf("会议室 %s 在排除列表中，跳过", room.Name))
			continue
		}
		if s.isRoomCapacityExceeded(room.Capacity) {
			s.Log.Debug(fmt.Sprintf("会议室 %s 容量 %d 超过限制 %d，跳过", room.Name, room.Capacity, s.Cfg.RoomSize))
			continue
		}
		if s.isRoomDisabled(room, date, startTime) {
			s.Log.Debug(fmt.Sprintf("会议室 %s 在预订时间段内被禁用，跳过", room.Name))
			continue
		}
		if !s.checkRoomReservePolicy(ctx, room.ID, room.Name, date, startTime, endTime) {
			continue
		}
		if available, _ := s.API.GetRoomFreeBusy(ctx, room.ID, start, end); available {
			levelName := "未知楼层"
			for _, level := range childLevels {
				if level.RoomLevelID == room.RoomLevelID {
					levelName = level.Name
					break
				}
			}
			s.Log.Info(fmt.Sprintf("找到可用会议室: %s (楼层: %s)", room.Name, levelName))
			return &feishu.Room{
				ID:           room.ID,
				Name:         room.Name,
				Capacity:     room.Capacity,
				Description:  room.Description,
				Status:       1,
				CustomRoomID: room.CustomRoomID,
			}
		}
		s.Log.Debug(fmt.Sprintf("会议室 %s 在预订时间段内不可用，跳过", room.Name))
	}

	s.Log.Warn(fmt.Sprintf("在层级 %s 下未找到可用会议室: %s", s.Cfg.RoomLevelID, strings.Join(roomNames, ", ")))
	return nil
}

// sortRoomsByPriorityAndLevel 按 ROOM_LIST 优先级（精确匹配 > 部分匹配 > 未匹配）
// 与楼层顺序排序会议室。
func sortRoomsByPriorityAndLevel(rooms []feishu.RoomDetail, roomNames []string, childLevels []feishu.RoomLevel) []feishu.RoomDetail {
	nameIndex := func(room *feishu.RoomDetail) int {
		for i, name := range roomNames {
			if name == room.Name || name == room.CustomRoomID {
				return i
			}
		}
		for i, name := range roomNames {
			if strings.Contains(room.Name, name) ||
				(room.CustomRoomID != "" && strings.Contains(room.CustomRoomID, name)) {
				return i + len(roomNames)
			}
		}
		return len(roomNames) * 2
	}
	levelIndex := func(room *feishu.RoomDetail) int {
		for i, level := range childLevels {
			if level.RoomLevelID == room.RoomLevelID {
				return i
			}
		}
		return -1
	}

	sorted := make([]feishu.RoomDetail, len(rooms))
	copy(sorted, rooms)
	sort.SliceStable(sorted, func(i, j int) bool {
		ni, nj := nameIndex(&sorted[i]), nameIndex(&sorted[j])
		if ni != nj {
			return ni < nj
		}
		return levelIndex(&sorted[i]) < levelIndex(&sorted[j])
	})
	return sorted
}

func (s *Service) shouldExcludeRoom(roomName string) bool {
	for _, keyword := range s.Cfg.RoomExcludeList {
		if strings.Contains(roomName, keyword) {
			return true
		}
	}
	return false
}

func (s *Service) isRoomCapacityExceeded(capacity int) bool {
	return s.Cfg.RoomSize > 0 && capacity > s.Cfg.RoomSize
}

// isRoomDisabled 预订开始时刻是否落在会议室禁用时间窗内。
func (s *Service) isRoomDisabled(room *feishu.RoomDetail, date, startTime string) bool {
	bookingDateTime, err := time.ParseInLocation("2006-01-02 15:04:05", date+" "+startTime, s.Loc)
	if err != nil {
		return false
	}
	bookingTimestamp := bookingDateTime.Unix()

	disableStart, _ := strconv.ParseInt(room.RoomStatus.DisableStartTime, 10, 64)
	disableEnd, _ := strconv.ParseInt(room.RoomStatus.DisableEndTime, 10, 64)

	if disableStart > 0 && disableEnd > 0 {
		return bookingTimestamp >= disableStart && bookingTimestamp <= disableEnd
	}
	if disableStart > 0 && disableEnd == 0 {
		return bookingTimestamp >= disableStart
	}
	return false
}

// checkDaysInAdvance 预检：预订日期是否超出可提前预订天数。
func (s *Service) checkDaysInAdvance(ctx context.Context, probeRoomID, date string) bool {
	config, err := s.API.GetRoomReserveConfig(ctx, probeRoomID)
	if err != nil || config == nil || config.TimeSwitch != 1 || config.DaysInAdvance == nil {
		return true
	}
	daysAhead := s.daysAhead(date)
	if daysAhead > *config.DaysInAdvance {
		s.Log.Warn(fmt.Sprintf("预订日期 %s 超出可提前预订天数（%d > %d），跳过所有会议室", date, daysAhead, *config.DaysInAdvance))
		return false
	}
	return true
}

func (s *Service) checkRoomReservePolicy(ctx context.Context, roomID, roomName, date, startTime, endTime string) bool {
	config, err := s.API.GetRoomReserveConfig(ctx, roomID)
	if err != nil || config == nil || config.TimeSwitch != 1 {
		return true
	}

	if config.DaysInAdvance != nil && *config.DaysInAdvance >= 0 {
		daysAhead := s.daysAhead(date)
		if daysAhead > *config.DaysInAdvance {
			s.Log.Debug(fmt.Sprintf("会议室 %s 预定策略不允许提前 %d 天预定（最多 %d 天），跳过", roomName, daysAhead, *config.DaysInAdvance))
			return false
		}
	}

	start, end := s.bookingRange(date, startTime, endTime)

	if config.MaxDuration != nil && *config.MaxDuration > 0 {
		durationHours := end.Sub(start).Hours()
		if durationHours > float64(*config.MaxDuration) {
			s.Log.Debug(fmt.Sprintf("会议室 %s 预定策略不允许超过 %d 小时的会议（当前 %.1f 小时），跳过", roomName, *config.MaxDuration, durationHours))
			return false
		}
	}

	if config.StartTime != "" && config.EndTime != "" {
		windowStart, errStart := strconv.Atoi(config.StartTime)
		windowEnd, errEnd := strconv.Atoi(config.EndTime)
		if errStart == nil && errEnd == nil && windowEnd > 0 {
			dayStart := startOfDay(start)
			bookingStartSeconds := int(start.Sub(dayStart).Seconds())
			bookingEndSeconds := int(end.Sub(dayStart).Seconds())
			if bookingStartSeconds < windowStart || bookingEndSeconds > windowEnd {
				s.Log.Debug(fmt.Sprintf("会议室 %s 预定策略不允许在该时间段预定（允许: %d-%d 秒），跳过", roomName, windowStart, windowEnd))
				return false
			}
		}
	}

	return true
}

func (s *Service) getMaxBookingDays(ctx context.Context) int {
	var firstRoomID string

	if s.Cfg.RoomLevelID != "" {
		rooms, err := s.API.GetRoomsByLevel(ctx, s.Cfg.RoomLevelID)
		if err == nil {
			for _, room := range rooms {
				if room.RoomStatus.Status {
					firstRoomID = room.ID
					break
				}
			}
		}
	} else {
		rooms, err := s.API.SearchRooms(ctx)
		if err == nil {
			for _, room := range rooms {
				if room.Status == 1 {
					firstRoomID = room.ID
					break
				}
			}
		}
	}

	if firstRoomID == "" {
		s.Log.Warn(fmt.Sprintf("无法获取会议室列表，使用默认最大预订天数: %d", defaultMaxBookingDays))
		return defaultMaxBookingDays
	}

	config, err := s.API.GetRoomReserveConfig(ctx, firstRoomID)
	if err == nil && config != nil && config.TimeSwitch == 1 && config.DaysInAdvance != nil {
		s.Log.Info(fmt.Sprintf("使用会议室预定策略的提前天数: %d 天", *config.DaysInAdvance))
		return *config.DaysInAdvance
	}
	return defaultMaxBookingDays
}

// daysAhead 预订日期相对今天的整天数差。
func (s *Service) daysAhead(date string) int {
	bookingDay, err := time.ParseInLocation("2006-01-02", date, s.Loc)
	if err != nil {
		return 0
	}
	today := startOfDay(s.Clock().In(s.Loc))
	return int(bookingDay.Sub(today).Hours() / 24)
}

func (s *Service) bookingRange(date, startTime, endTime string) (time.Time, time.Time) {
	start, _ := time.ParseInLocation("2006-01-02 15:04:05", date+" "+startTime, s.Loc)
	end, _ := time.ParseInLocation("2006-01-02 15:04:05", date+" "+endTime, s.Loc)
	return start, end
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func endOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, t.Location())
}

// dateAfter 按日期（忽略时刻）比较 a 是否晚于 b。
func dateAfter(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	if ay != by {
		return ay > by
	}
	if am != bm {
		return am > bm
	}
	return ad > bd
}
