package config

import (
	"strings"
	"testing"
)

func findField(t *testing.T, groups []GroupSpec, envKey string) FieldSpec {
	t.Helper()
	for _, g := range groups {
		for _, f := range g.Fields {
			if f.Item.EnvKey == envKey {
				return f
			}
		}
	}
	t.Fatalf("表单缺少 %s", envKey)
	return FieldSpec{}
}

func TestBuildFormSpecCoversRegistry(t *testing.T) {
	groups := BuildFormSpec(map[string]string{}, nil)
	if len(groups) != 4 {
		t.Fatalf("分组数 = %d, want 4", len(groups))
	}
	seen := map[string]int{}
	for _, g := range groups {
		for _, f := range g.Fields {
			seen[f.Item.EnvKey]++
		}
	}
	for _, it := range Registry {
		want := 1
		if it.EnvKey == "SENTRY_DSN" {
			want = 0 // sentry.dsn 不进 TUI,用 room config set 管理
		}
		if seen[it.EnvKey] != want {
			t.Errorf("%s 在表单中出现 %d 次, want %d", it.EnvKey, seen[it.EnvKey], want)
		}
	}
}

// 字段单行展示:「文案[（必填）]  键名」,不再有第二行描述。
func TestBuildFormSpecFieldTitles(t *testing.T) {
	groups := BuildFormSpec(map[string]string{}, nil)
	appID := findField(t, groups, "FEISHU_APP_ID")
	if appID.Title != appID.Item.Desc+"（必填）  feishu.app_id" {
		t.Errorf("必填项 Title 应为「文案（必填）  键名」: %q", appID.Title)
	}
	size := findField(t, groups, "ROOM_SIZE")
	if size.Title != size.Item.Desc+"  booking.room_size" {
		t.Errorf("选填项 Title 应为「文案  键名」: %q", size.Title)
	}
}

func TestBuildFormSpecFieldKinds(t *testing.T) {
	groups := BuildFormSpec(map[string]string{}, nil)
	tests := []struct {
		env  string
		kind FieldKind
	}{
		{"FEISHU_AUTH_MODE", FieldSelect},
		{"TASK_FORMAT", FieldText},
		{"FEISHU_APP_ID", FieldInput},
		{"FEISHU_APP_SECRET", FieldInput},
	}
	for _, tt := range tests {
		if f := findField(t, groups, tt.env); f.Kind != tt.kind {
			t.Errorf("%s Kind = %v, want %v", tt.env, f.Kind, tt.kind)
		}
	}
	if f := findField(t, groups, "FEISHU_APP_SECRET"); !f.Masked {
		t.Error("app_secret 应掩码输入")
	}
	if f := findField(t, groups, "FEISHU_APP_ID"); f.Masked {
		t.Error("app_id 不应掩码")
	}
	if f := findField(t, groups, "FEISHU_AUTH_MODE"); len(f.Options) != 3 || f.Options[0] != (Option{Label: "auto", Value: "auto"}) {
		t.Errorf("auth_mode 选项应为 label==value 的枚举 = %v", f.Options)
	}
}

// room_level_id:注入层级选项时为下拉(首项「不限」空值),未注入时保持文本输入。
func TestBuildFormSpecLevelOptions(t *testing.T) {
	if f := findField(t, BuildFormSpec(map[string]string{}, nil), "ROOM_LEVEL_ID"); f.Kind != FieldInput {
		t.Errorf("无层级选项时应为文本输入, got %v", f.Kind)
	}

	levels := []Option{{Label: "集团", Value: "L1"}, {Label: "集团 / A座", Value: "L2"}}
	f := findField(t, BuildFormSpec(map[string]string{"ROOM_LEVEL_ID": "L2"}, levels), "ROOM_LEVEL_ID")
	if f.Kind != FieldSelect {
		t.Fatalf("注入层级后应为下拉, got %v", f.Kind)
	}
	if f.Options[0] != (Option{Label: "（不限）", Value: ""}) {
		t.Errorf("首项应为「（不限）」空值: %+v", f.Options[0])
	}
	if len(f.Options) != 3 || f.Initial != "L2" {
		t.Errorf("选项应为 不限+层级树 且保留生效值: options=%v initial=%q", f.Options, f.Initial)
	}

	// 生效值不在树中(层级已删除):追加「(当前值)」项,否则 huh 会在用户直接
	// 走过表单时把配置静默改成首个选项
	f = findField(t, BuildFormSpec(map[string]string{"ROOM_LEVEL_ID": "gone"}, levels), "ROOM_LEVEL_ID")
	last := f.Options[len(f.Options)-1]
	if last.Value != "gone" || !strings.Contains(last.Label, "当前值") {
		t.Errorf("陈旧生效值应以「当前值」项保留: %v", f.Options)
	}
}

func TestBuildFormSpecInitial(t *testing.T) {
	groups := BuildFormSpec(map[string]string{"FEISHU_APP_ID": "cli_x"}, nil)
	if f := findField(t, groups, "FEISHU_APP_ID"); f.Initial != "cli_x" {
		t.Errorf("app_id Initial = %q", f.Initial)
	}
	// 生效值缺失时 Select 回退默认,保证控件有合法初值
	if f := findField(t, groups, "FEISHU_AUTH_MODE"); f.Initial != "auto" {
		t.Errorf("auth_mode Initial = %q, want auto", f.Initial)
	}
}

func TestBuildFormSpecValidate(t *testing.T) {
	groups := BuildFormSpec(map[string]string{}, nil)
	appID := findField(t, groups, "FEISHU_APP_ID")
	if err := appID.Validate(""); err == nil {
		t.Error("必填项空值应校验失败")
	}
	if err := appID.Validate("cli_x"); err != nil {
		t.Errorf("必填项非空应通过: %v", err)
	}
	size := findField(t, groups, "ROOM_SIZE")
	if err := size.Validate(""); err != nil {
		t.Errorf("可选项空值应通过(视为不设置): %v", err)
	}
	if err := size.Validate("abc"); err == nil {
		t.Error("非法整数应校验失败")
	}
}

func TestApplyFormResult(t *testing.T) {
	doc := NewDocument()
	doc.Values["SENTRY_DSN"] = "" // 显式空
	doc.Values["TIANAPI_KEY"] = "old_key"
	doc.Values["ROOM_LEVEL_ID"] = "lvl_old"

	err := ApplyFormResult(doc, map[string]string{
		"SENTRY_DSN":    "",        // 显式空保留
		"TIANAPI_KEY":   "",        // 已设置的清空 = unset
		"ROOM_LEVEL_ID": "lvl_new", // 覆盖
		"ROOM_LIST":     " A , B ", // 规范化
		"EMAIL_DOMAIN":  "",        // 本就未设置,保持未设置
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := doc.Values["SENTRY_DSN"]; !ok || v != "" {
		t.Error("显式空应保留")
	}
	if _, ok := doc.Values["TIANAPI_KEY"]; ok {
		t.Error("清空已设置项应删除")
	}
	if doc.Values["ROOM_LEVEL_ID"] != "lvl_new" {
		t.Errorf("覆盖失败: %v", doc.Values)
	}
	if doc.Values["ROOM_LIST"] != "A,B" {
		t.Errorf("list 未规范化: %q", doc.Values["ROOM_LIST"])
	}
	if _, ok := doc.Values["EMAIL_DOMAIN"]; ok {
		t.Error("未设置项的空值不应写入")
	}

	if err := ApplyFormResult(doc, map[string]string{"ROOM_SIZE": "abc"}); err == nil {
		t.Error("非法值应报错")
	}
}
