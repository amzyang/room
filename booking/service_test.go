package booking

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/amzyang/room/feishu"
	"github.com/amzyang/room/logx"
)

// fakeAPI 内存实现 booking.API，各方法返回可配置。
type fakeAPI struct {
	rooms        []feishu.Room
	events       []feishu.CalendarEvent
	freeBusy     bool
	bookEventID  string
	bookErr      error
	deleteErr    error
	bookedRoomID string
}

func (f *fakeAPI) VerifyCredentials(context.Context) error { return nil }
func (f *fakeAPI) SearchRooms(context.Context) ([]feishu.Room, error) {
	return f.rooms, nil
}
func (f *fakeAPI) GetRoomLevelChildren(context.Context, string) ([]feishu.RoomLevel, error) {
	return nil, nil
}
func (f *fakeAPI) GetRoomsByLevel(context.Context, string) ([]feishu.RoomDetail, error) {
	return nil, nil
}
func (f *fakeAPI) GetRoomReserveConfig(context.Context, string) (*feishu.ReserveConfig, error) {
	return nil, nil
}
func (f *fakeAPI) GetPrimaryCalendar(context.Context) (string, error) { return "cal_me", nil }
func (f *fakeAPI) GetCalendarEvents(context.Context, time.Time, time.Time) ([]feishu.CalendarEvent, error) {
	return f.events, nil
}
func (f *fakeAPI) DeleteCalendarEvent(_ context.Context, _ string) error { return f.deleteErr }
func (f *fakeAPI) GetRoomFreeBusy(context.Context, string, time.Time, time.Time) (bool, error) {
	return f.freeBusy, nil
}
func (f *fakeAPI) FindUsersByEmails(context.Context, []string) ([]feishu.User, error) {
	return nil, nil
}
func (f *fakeAPI) BookRoomWithEvent(_ context.Context, _ feishu.Event, roomID string, _ []string) (string, error) {
	f.bookedRoomID = roomID
	return f.bookEventID, f.bookErr
}

func newTestService(t *testing.T, api *fakeAPI) *Service {
	t.Helper()
	loc, _ := time.LoadLocation("Asia/Shanghai")
	dir := t.TempDir()
	auto := &AutoBookingCache{Path: filepath.Join(dir, "auto.json")}
	auto.Load()
	users := &UserIDCache{Path: filepath.Join(dir, "users.json")}
	users.Load()
	return &Service{
		API:       api,
		Cfg:       Config{RoomList: []string{"3F-A"}, TaskOwner: "owner"},
		Log:       logx.New(false, loc),
		Clock:     func() time.Time { return time.Date(2026, 7, 10, 9, 0, 0, 0, loc) },
		Loc:       loc,
		UserIDs:   users,
		AutoCache: auto,
		holidays:  map[string]bool{},
	}
}

func TestBookRoomBooked(t *testing.T) {
	api := &fakeAPI{
		rooms:       []feishu.Room{{ID: "omm_1", Name: "3F-A", Status: 1}},
		freeBusy:    true,
		bookEventID: "evt_123",
	}
	s := newTestService(t, api)

	got, err := s.BookRoom(context.Background(), "2026-07-15", "14:00:00", "15:00:00", "周会", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusBooked {
		t.Fatalf("Status = %s, want booked", got.Status)
	}
	if got.EventID != "evt_123" || got.Room == nil || got.Room.ID != "omm_1" || got.Room.Name != "3F-A" {
		t.Errorf("结果缺少事件与房间信息: %+v", got)
	}
	if got.Date != "2026-07-15" || got.StartTime != "14:00:00" || got.EndTime != "15:00:00" || got.Title != "周会" {
		t.Errorf("结果时间/标题不符: %+v", got)
	}
	if api.bookedRoomID != "omm_1" {
		t.Errorf("实际预订房间 = %s", api.bookedRoomID)
	}
	if !s.AutoCache.Has("evt_123") {
		t.Error("成功预订应记入 AutoCache")
	}
}

func TestBookRoomHolidaySkipped(t *testing.T) {
	s := newTestService(t, &fakeAPI{})
	s.holidays["2026-10-01"] = true

	got, err := s.BookRoom(context.Background(), "2026-10-01", "14:00:00", "15:00:00", "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusHolidaySkipped {
		t.Errorf("Status = %s, want holiday_skipped", got.Status)
	}
	if got.EventID != "" || got.Room != nil {
		t.Errorf("未预订不应带事件/房间: %+v", got)
	}
}

func TestBookRoomConflict(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	overlap := feishu.CalendarEvent{
		EventID:        "evt_other",
		Summary:        "已有会议",
		StartTimestamp: time.Date(2026, 7, 15, 14, 30, 0, 0, loc).Unix(),
		EndTimestamp:   time.Date(2026, 7, 15, 15, 30, 0, 0, loc).Unix(),
	}
	s := newTestService(t, &fakeAPI{events: []feishu.CalendarEvent{overlap}})

	got, err := s.BookRoom(context.Background(), "2026-07-15", "14:00:00", "15:00:00", "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusConflict {
		t.Errorf("Status = %s, want conflict", got.Status)
	}
}

func TestBookRoomNoRoom(t *testing.T) {
	// 有房间但时段全忙 → no_room
	s := newTestService(t, &fakeAPI{
		rooms:    []feishu.Room{{ID: "omm_1", Name: "3F-A", Status: 1}},
		freeBusy: false,
	})

	got, err := s.BookRoom(context.Background(), "2026-07-15", "14:00:00", "15:00:00", "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusNoRoom {
		t.Errorf("Status = %s, want no_room", got.Status)
	}
}

func TestBookRoomAPIErrorIsError(t *testing.T) {
	boom := errors.New("api down")
	s := newTestService(t, &fakeAPI{
		rooms:       []feishu.Room{{ID: "omm_1", Name: "3F-A", Status: 1}},
		freeBusy:    true,
		bookEventID: "",
		bookErr:     boom,
	})

	_, err := s.BookRoom(context.Background(), "2026-07-15", "14:00:00", "15:00:00", "t", nil)
	if !errors.Is(err, boom) {
		t.Errorf("API 错误应向上传递: %v", err)
	}
}

func TestCancelEvent(t *testing.T) {
	s := newTestService(t, &fakeAPI{})
	got, err := s.CancelEvent(context.Background(), "evt_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.AlreadyCancelled {
		t.Error("首次取消不应标记 AlreadyCancelled")
	}
}

func TestCancelEventIdempotent(t *testing.T) {
	s := newTestService(t, &fakeAPI{deleteErr: &feishu.APIError{Code: 193003, Msg: "deleted"}})
	got, err := s.CancelEvent(context.Background(), "evt_1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.AlreadyCancelled {
		t.Error("已删除事件应幂等成功并标记 AlreadyCancelled")
	}
}

func TestCancelEventError(t *testing.T) {
	s := newTestService(t, &fakeAPI{deleteErr: errors.New("boom")})
	if _, err := s.CancelEvent(context.Background(), "evt_1"); err == nil {
		t.Error("非幂等错误应向上传递")
	}
}
