package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/amzyang/room/feishu"
	"github.com/amzyang/room/output"
)

// fakeLevelLister 内存层级树，记录每次请求的 parentID。
type fakeLevelLister struct {
	children map[string][]feishu.RoomLevel
	calls    []string
	err      error
}

func (f *fakeLevelLister) GetRoomLevelChildren(_ context.Context, parentID string) ([]feishu.RoomLevel, error) {
	f.calls = append(f.calls, parentID)
	if f.err != nil {
		return nil, f.err
	}
	return f.children[parentID], nil
}

// testLevelTree 根 → 集团(L1) → { A座(L2) → { 3层(L3), 4层(L4) }, 多功能厅(R1) }。
func testLevelTree() *fakeLevelLister {
	return &fakeLevelLister{children: map[string][]feishu.RoomLevel{
		"": {{RoomLevelID: "L1", Name: "集团", HasChild: true}},
		"L1": {
			{RoomLevelID: "L2", Name: "A座", HasChild: true, ParentID: "L1"},
			{RoomLevelID: "R1", Name: "多功能厅", ParentID: "L1"},
		},
		"L2": {
			{RoomLevelID: "L3", Name: "3层", ParentID: "L2"},
			{RoomLevelID: "L4", Name: "4层", ParentID: "L2"},
		},
	}}
}

// scriptedSelect 按 label 顺序依次选择；label 不在菜单中则测试失败。
func scriptedSelect(t *testing.T, labels ...string) selectLevelFunc {
	t.Helper()
	return func(_ string, opts []levelOption) (levelOption, error) {
		if len(labels) == 0 {
			t.Fatal("选择脚本已耗尽，仍在弹出菜单")
		}
		want := labels[0]
		labels = labels[1:]
		for _, o := range opts {
			if o.Label == want {
				return o, nil
			}
		}
		var got []string
		for _, o := range opts {
			got = append(got, o.Label)
		}
		t.Fatalf("菜单中找不到 %q，实际选项: %v", want, got)
		return levelOption{}, nil
	}
}

func TestBuildLevelMenuRoot(t *testing.T) {
	opts := buildLevelMenu(nil, []feishu.RoomLevel{
		{RoomLevelID: "L1", Name: "集团", HasChild: true},
		{RoomLevelID: "R1", Name: "多功能厅"},
	})
	if len(opts) != 2 {
		t.Fatalf("根层级菜单不应含「就选这一级/返回上一级」，实际 %d 项: %v", len(opts), opts)
	}
	if opts[0].Label != "集团 ▸" || opts[0].Action != levelActionEnter {
		t.Errorf("有子级的节点应为下钻项: %+v", opts[0])
	}
	if opts[1].Label != "多功能厅" || opts[1].Action != levelActionPick {
		t.Errorf("叶子节点应为直选项: %+v", opts[1])
	}
}

func TestBuildLevelMenuNested(t *testing.T) {
	path := []feishu.RoomLevel{{RoomLevelID: "L1", Name: "集团"}, {RoomLevelID: "L2", Name: "A座"}}
	opts := buildLevelMenu(path, []feishu.RoomLevel{{RoomLevelID: "L3", Name: "3层"}})
	if len(opts) != 3 {
		t.Fatalf("非根菜单应为 就选这一级+返回上一级+子级，实际: %v", opts)
	}
	if !strings.Contains(opts[0].Label, "A座") || opts[0].Action != levelActionPick || opts[0].Level.RoomLevelID != "L2" {
		t.Errorf("首项应为选定当前层级 A座: %+v", opts[0])
	}
	if opts[1].Action != levelActionUp {
		t.Errorf("次项应为返回上一级: %+v", opts[1])
	}
}

func TestLevelPathTitle(t *testing.T) {
	if got := levelPathTitle(nil); !strings.Contains(got, "根层级") {
		t.Errorf("根路径标题: %q", got)
	}
	path := []feishu.RoomLevel{{Name: "集团"}, {Name: "A座"}}
	if got := levelPathTitle(path); !strings.Contains(got, "集团 / A座") {
		t.Errorf("嵌套路径标题: %q", got)
	}
}

func TestPickRoomLevelLeafDirect(t *testing.T) {
	api := testLevelTree()
	level, err := pickRoomLevel(context.Background(), api, scriptedSelect(t, "集团 ▸", "多功能厅"))
	if err != nil {
		t.Fatal(err)
	}
	if level.RoomLevelID != "R1" {
		t.Errorf("应选中叶子 R1，实际: %+v", level)
	}
	want := []string{"", "L1"}
	if strings.Join(api.calls, ",") != strings.Join(want, ",") {
		t.Errorf("API 调用序列不符: %v", api.calls)
	}
}

func TestPickRoomLevelNavigateUpAndPickCurrent(t *testing.T) {
	api := testLevelTree()
	level, err := pickRoomLevel(context.Background(), api,
		scriptedSelect(t, "集团 ▸", "A座 ▸", ".. 返回上一级", "✔ 就选这一级（集团）"))
	if err != nil {
		t.Fatal(err)
	}
	if level.RoomLevelID != "L1" {
		t.Errorf("应选中当前层级 L1，实际: %+v", level)
	}
	want := []string{"", "L1", "L2", "L1"}
	if strings.Join(api.calls, ",") != strings.Join(want, ",") {
		t.Errorf("API 调用序列不符: %v", api.calls)
	}
}

func TestPickRoomLevelRootEmpty(t *testing.T) {
	api := &fakeLevelLister{children: map[string][]feishu.RoomLevel{}}
	_, err := pickRoomLevel(context.Background(), api, scriptedSelect(t))
	if err == nil || !strings.Contains(err.Error(), "层级") {
		t.Errorf("根层级为空应报错，实际: %v", err)
	}
}

func TestPickRoomLevelAPIError(t *testing.T) {
	boom := errors.New("boom")
	api := &fakeLevelLister{err: boom}
	_, err := pickRoomLevel(context.Background(), api, scriptedSelect(t))
	if !errors.Is(err, boom) {
		t.Errorf("API 错误应向上传递，实际: %v", err)
	}
}

func TestConfigSetSingleArgOnlyRoomLevel(t *testing.T) {
	a := newConfigTestApp(t.TempDir()+"/config.toml", nil)
	_, _, err := execConfigCmd(t, a, "set", "booking.task_owner")
	if err == nil {
		t.Fatal("其他 key 省略 VALUE 应报错")
	}
	e := output.Classify(err)
	if e.Type != output.TypeValidation || !strings.Contains(e.Hint, "booking.room_level_id") {
		t.Errorf("应归 validation 且 hint 提示仅 room_level_id 支持，实际: type=%s hint=%q", e.Type, e.Hint)
	}
}

func TestConfigSetPickMissingCredentials(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "")
	t.Setenv("FEISHU_APP_SECRET", "")
	a := newConfigTestApp(t.TempDir()+"/config.toml", nil)
	_, _, err := execConfigCmd(t, a, "set", "booking.room_level_id")
	if err == nil || !strings.Contains(err.Error(), "FEISHU_APP_ID") {
		t.Errorf("缺凭证应报错并指向配置方式，实际: %v", err)
	}
}

func TestConfigSetPickRequiresTerminal(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "cli_x")
	t.Setenv("FEISHU_APP_SECRET", "secret_x")
	a := newConfigTestApp(t.TempDir()+"/config.toml", nil)
	_, _, err := execConfigCmd(t, a, "set", "booking.room_level_id")
	if err == nil || !strings.Contains(err.Error(), "终端") {
		t.Errorf("非终端环境应报错，实际: %v", err)
	}
}
