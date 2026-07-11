// Package feishu 封装飞书开放平台 API（基于官方 oapi-sdk-go/v3）、
// OAuth 设备码鉴权与自定义机器人 webhook。
package feishu

// Room 会议室（搜索接口返回的精简形态）。
type Room struct {
	ID           string
	Name         string
	Capacity     int
	Description  string
	Status       int // 1 = 启用
	CustomRoomID string
}

// RoomStatus 会议室启用/禁用状态；禁用时间为 unix 秒的字符串（"0" 表示未设置/永久）。
type RoomStatus struct {
	Status           bool
	DisableStartTime string
	DisableEndTime   string
}

// RoomDetail 按层级查询返回的会议室详情。
type RoomDetail struct {
	ID           string
	Name         string
	Capacity     int
	Description  string
	CustomRoomID string
	RoomLevelID  string
	RoomStatus   RoomStatus
}

// RoomLevel 会议室层级（楼层）。
type RoomLevel struct {
	RoomLevelID string
	Name        string
	HasChild    bool
	ParentID    string
}

// ReserveConfig 会议室预定策略；指针字段区分「未设置」。
type ReserveConfig struct {
	TimeSwitch    int
	DaysInAdvance *int
	StartTime     string // 每日可预定窗口开始（当日秒数）
	EndTime       string // 每日可预定窗口结束（当日秒数）
	MaxDuration   *int   // 单次预定时长上限（小时）
}

// User 通讯录用户。
type User struct {
	ID    string
	Name  string
	Email string
}

// Event 待创建的日程；时间为 unix 秒。
type Event struct {
	Title           string
	Description     string
	StartTime       int64
	EndTime         int64
	Timezone        string
	Location        string
	ReminderMinutes int
	Recurrence      string
}

// CalendarEvent 日历事件列表项；时间戳为 unix 秒，缺失时为 0。
type CalendarEvent struct {
	EventID             string
	Summary             string
	Description         string
	StartTimestamp      int64
	EndTimestamp        int64
	LocationName        string
	Status              string
	OrganizerCalendarID string
}

// Attendee 日程参与人。
type Attendee struct {
	Type       string // user / resource / chat
	UserID     string
	RoomID     string
	ChatID     string
	RsvpStatus string
}
