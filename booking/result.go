package booking

// BookStatus 单次预订的结果状态。字符串值同时用于 --json 输出与错误类型，
// 保持同一词汇表（booked / holiday_skipped / conflict / no_room）。
type BookStatus string

const (
	StatusBooked         BookStatus = "booked"
	StatusHolidaySkipped BookStatus = "holiday_skipped"
	StatusConflict       BookStatus = "conflict"
	StatusNoRoom         BookStatus = "no_room"
	// 以下仅出现在 auto 批量结果中
	StatusPlanned BookStatus = "planned" // 演练模式（--dryrun）：计划预订但未执行
	StatusFailed  BookStatus = "failed"  // 预订调用出错（详见 stderr 日志）
)

// BookedRoom 选中的会议室。
type BookedRoom struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// BookResult 单次预订结果。核心不变式：Status == StatusBooked ⟺ 房间订上了。
type BookResult struct {
	Status               BookStatus  `json:"status"`
	EventID              string      `json:"event_id,omitempty"`
	Room                 *BookedRoom `json:"room,omitempty"` // 仅 booked 时非 nil
	Date                 string      `json:"date"`
	StartTime            string      `json:"start_time"`
	EndTime              string      `json:"end_time"`
	Title                string      `json:"title,omitempty"`
	ParticipantsResolved int         `json:"participants_resolved"`
}

// CancelOutcome 取消结果；AlreadyCancelled 表示幂等命中（事件已被取消/删除）。
type CancelOutcome struct {
	AlreadyCancelled bool
}
