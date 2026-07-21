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
	rooms         []feishu.Room
	events        []feishu.CalendarEvent
	freeBusy      bool
	bookEventID   string
	bookErr       error
	deleteErr     error
	bookedRoomID  string
	bookedUserIDs []string
	bookCalls     int

	currentUser   *feishu.UserIdentity
	noCurrentUser bool // 模拟未登录：newTestService 不注入默认身份

	foundUsers []feishu.User // FindUsersByEmails 的返回值
	findEmails []string      // FindUsersByEmails 收到的邮箱
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
func (f *fakeAPI) FindUsersByEmails(_ context.Context, emails []string) ([]feishu.User, error) {
	f.findEmails = append(f.findEmails, emails...)
	return f.foundUsers, nil
}
func (f *fakeAPI) BookRoomWithEvent(_ context.Context, _ feishu.Event, roomID string, userIDs []string) (string, error) {
	f.bookCalls++
	f.bookedRoomID = roomID
	f.bookedUserIDs = userIDs
	return f.bookEventID, f.bookErr
}
func (f *fakeAPI) CurrentUser(context.Context) *feishu.UserIdentity { return f.currentUser }

func newTestService(t *testing.T, api *fakeAPI) *Service {
	t.Helper()
	loc, _ := time.LoadLocation("Asia/Shanghai")
	dir := t.TempDir()
	auto := &AutoBookingCache{Path: filepath.Join(dir, "auto.json")}
	auto.Load()
	users := &UserIDCache{Path: filepath.Join(dir, "users.json")}
	users.Load()
	if api.currentUser == nil && !api.noCurrentUser {
		api.currentUser = &feishu.UserIdentity{OpenID: "ou_me", UserID: "u_me", Name: "我"}
	}
	return &Service{
		API:       api,
		Cfg:       Config{RoomList: []string{"3F-A"}},
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

func bookableAPI() *fakeAPI {
	return &fakeAPI{
		rooms:       []feishu.Room{{ID: "omm_1", Name: "3F-A", Status: 1}},
		freeBusy:    true,
		bookEventID: "evt_1",
	}
}

func TestBookRoomAppendsAuthorizedUser(t *testing.T) {
	api := bookableAPI()
	s := newTestService(t, api)

	got, err := s.BookRoom(context.Background(), "2026-07-15", "14:00:00", "15:00:00", "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusBooked {
		t.Fatalf("Status = %s, want booked", got.Status)
	}
	if len(api.bookedUserIDs) != 1 || api.bookedUserIDs[0] != "u_me" {
		t.Errorf("授权用户应自动加入参会人: %v", api.bookedUserIDs)
	}
	if got.ParticipantsResolved != 1 {
		t.Errorf("ParticipantsResolved = %d, want 1", got.ParticipantsResolved)
	}
}

func TestBookRoomSelfDeduplicated(t *testing.T) {
	api := bookableAPI()
	s := newTestService(t, api)
	if err := s.UserIDs.Set("me", "u_me"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.BookRoom(context.Background(), "2026-07-15", "14:00:00", "15:00:00", "t", []string{"me"}); err != nil {
		t.Fatal(err)
	}
	if len(api.bookedUserIDs) != 1 || api.bookedUserIDs[0] != "u_me" {
		t.Errorf("本人已在参会人列表时不应重复追加: %v", api.bookedUserIDs)
	}
}

func TestBookRoomNoParticipantsAborts(t *testing.T) {
	api := bookableAPI()
	api.noCurrentUser = true
	s := newTestService(t, api)

	got, err := s.BookRoom(context.Background(), "2026-07-15", "14:00:00", "15:00:00", "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusNoParticipants {
		t.Fatalf("Status = %s, want no_participants", got.Status)
	}
	if api.bookCalls != 0 {
		t.Error("归零放弃不应触达预订 API")
	}
	if s.AutoCache.Has("evt_1") {
		t.Error("归零放弃不应写入 AutoCache")
	}
}

func TestBookRoomAllUnresolvedAborts(t *testing.T) {
	api := bookableAPI()
	api.noCurrentUser = true
	s := newTestService(t, api)

	got, err := s.BookRoom(context.Background(), "2026-07-15", "14:00:00", "15:00:00", "t", []string{"ghost"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusNoParticipants {
		t.Fatalf("Status = %s, want no_participants", got.Status)
	}
	if len(got.ParticipantsUnresolved) != 1 || got.ParticipantsUnresolved[0] != "ghost" {
		t.Errorf("ParticipantsUnresolved = %v, want [ghost]", got.ParticipantsUnresolved)
	}
}

func TestBookRoomGroupCountsAsHuman(t *testing.T) {
	api := bookableAPI()
	api.noCurrentUser = true
	s := newTestService(t, api)

	got, err := s.BookRoom(context.Background(), "2026-07-15", "14:00:00", "15:00:00", "t", []string{"oc_g1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusBooked {
		t.Fatalf("Status = %s, want booked（群参会人算有效）", got.Status)
	}
	if len(api.bookedUserIDs) != 1 || api.bookedUserIDs[0] != "oc_g1" {
		t.Errorf("bookedUserIDs = %v", api.bookedUserIDs)
	}
}

func TestBookRoomOpenIDOnlyIdentityAborts(t *testing.T) {
	api := bookableAPI()
	api.currentUser = &feishu.UserIdentity{OpenID: "ou_me"} // 权限降级：无 user_id
	s := newTestService(t, api)

	got, err := s.BookRoom(context.Background(), "2026-07-15", "14:00:00", "15:00:00", "t", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusNoParticipants {
		t.Errorf("Status = %s, want no_participants（open_id 无法作为 user_id 参会人）", got.Status)
	}
}

func TestBookRoomPartialResolveContinues(t *testing.T) {
	api := bookableAPI()
	s := newTestService(t, api)

	got, err := s.BookRoom(context.Background(), "2026-07-15", "14:00:00", "15:00:00", "t", []string{"ghost"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusBooked {
		t.Fatalf("Status = %s, want booked（部分解析失败仍继续）", got.Status)
	}
	if len(got.ParticipantsUnresolved) != 1 || got.ParticipantsUnresolved[0] != "ghost" {
		t.Errorf("ParticipantsUnresolved = %v, want [ghost]", got.ParticipantsUnresolved)
	}
	if got.ParticipantsResolved != 1 {
		t.Errorf("ParticipantsResolved = %d, want 1（仅授权用户）", got.ParticipantsResolved)
	}
}

func TestAutoBookNoParticipantsIsolatedPerTask(t *testing.T) {
	api := bookableAPI()
	api.noCurrentUser = true
	s := newTestService(t, api)
	s.Cfg.TaskFormat = "fri,11:00:00-12:00:00,weekly,ghost,A|fri,14:00:00-15:00:00,weekly,oc_g1,B"

	results, err := s.AutoBook(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("应产生批量结果")
	}
	for _, r := range results {
		switch r.Title {
		case "A":
			if r.Status != StatusNoParticipants {
				t.Errorf("任务 A 应为 no_participants: %+v", r)
			}
		case "B":
			if r.Status != StatusBooked {
				t.Errorf("任务 B 不应受任务 A 影响: %+v", r)
			}
		}
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

func TestResolveParticipantFullEmail(t *testing.T) {
	api := &fakeAPI{foundUsers: []feishu.User{{ID: "u_alice", Name: "Alice", Email: "alice@gaotu.cn"}}}
	s := newTestService(t, api)
	// EmailDomain 故意留空：完整邮箱不依赖该配置

	if got := s.resolveParticipantID(context.Background(), "alice@gaotu.cn"); got != "u_alice" {
		t.Fatalf("resolveParticipantID = %q, want u_alice", got)
	}
	if len(api.findEmails) != 1 || api.findEmails[0] != "alice@gaotu.cn" {
		t.Errorf("FindUsersByEmails 收到 %v, want [alice@gaotu.cn]", api.findEmails)
	}
	if s.UserIDs.Get("alice@gaotu.cn") != "u_alice" {
		t.Error("完整邮箱应写入用户ID缓存")
	}
}

func TestResolveParticipantPrefixWithDomain(t *testing.T) {
	api := &fakeAPI{foundUsers: []feishu.User{{ID: "u_bob", Name: "Bob"}}}
	s := newTestService(t, api)
	s.Cfg.EmailDomain = "gaotu.cn"

	if got := s.resolveParticipantID(context.Background(), "bob"); got != "u_bob" {
		t.Fatalf("resolveParticipantID = %q, want u_bob", got)
	}
	if len(api.findEmails) != 1 || api.findEmails[0] != "bob@gaotu.cn" {
		t.Errorf("FindUsersByEmails 收到 %v, want [bob@gaotu.cn]", api.findEmails)
	}
}

func TestResolveParticipantPrefixNeedsDomain(t *testing.T) {
	api := &fakeAPI{foundUsers: []feishu.User{{ID: "u_alice"}}}
	s := newTestService(t, api)

	if got := s.resolveParticipantID(context.Background(), "alice"); got != "" {
		t.Fatalf("未配置 EMAIL_DOMAIN 时前缀应解析失败, got %q", got)
	}
	if len(api.findEmails) != 0 {
		t.Errorf("不应发起邮箱查询: %v", api.findEmails)
	}
}

func TestResolveParticipantRejectsFeishuIDs(t *testing.T) {
	api := &fakeAPI{foundUsers: []feishu.User{{ID: "u_x"}}}
	s := newTestService(t, api)
	s.Cfg.EmailDomain = "gaotu.cn"

	for _, p := range []string{"ou_abc123", "on_abc123"} {
		if got := s.resolveParticipantID(context.Background(), p); got != "" {
			t.Errorf("%s 应被拒绝, got %q", p, got)
		}
	}
	if len(api.findEmails) != 0 {
		t.Errorf("ID 前缀不应发起邮箱查询: %v", api.findEmails)
	}
}

func TestResolveParticipantChatGroupPassthrough(t *testing.T) {
	s := newTestService(t, &fakeAPI{})
	if got := s.resolveParticipantID(context.Background(), "oc_group1"); got != "oc_group1" {
		t.Fatalf("oc_ 群组应原样透传, got %q", got)
	}
}
