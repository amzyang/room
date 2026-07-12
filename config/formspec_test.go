package config

import (
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
	groups := BuildFormSpec(map[string]string{})
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
		if seen[it.EnvKey] != 1 {
			t.Errorf("%s 在表单中出现 %d 次, want 1", it.EnvKey, seen[it.EnvKey])
		}
	}
}

func TestBuildFormSpecFieldKinds(t *testing.T) {
	groups := BuildFormSpec(map[string]string{})
	tests := []struct {
		env  string
		kind FieldKind
	}{
		{"FEISHU_AUTH_MODE", FieldSelect},
		{"TASK_FORMAT", FieldText},
		{"FEISHU_USER_AUTH_SCOPE", FieldText},
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
	if f := findField(t, groups, "FEISHU_AUTH_MODE"); len(f.Options) != 3 {
		t.Errorf("auth_mode 选项 = %v", f.Options)
	}
}

func TestBuildFormSpecInitial(t *testing.T) {
	groups := BuildFormSpec(map[string]string{"FEISHU_APP_ID": "cli_x"})
	if f := findField(t, groups, "FEISHU_APP_ID"); f.Initial != "cli_x" {
		t.Errorf("app_id Initial = %q", f.Initial)
	}
	// 生效值缺失时 Select 回退默认,保证控件有合法初值
	if f := findField(t, groups, "FEISHU_AUTH_MODE"); f.Initial != "auto" {
		t.Errorf("auth_mode Initial = %q, want auto", f.Initial)
	}
}

func TestBuildFormSpecValidate(t *testing.T) {
	groups := BuildFormSpec(map[string]string{})
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
	doc.Values["TASK_OWNER"] = "old_owner"
	doc.Values["ROOM_LEVEL_ID"] = "lvl_old"

	err := ApplyFormResult(doc, map[string]string{
		"SENTRY_DSN":    "",        // 显式空保留
		"TASK_OWNER":    "",        // 已设置的清空 = unset
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
	if _, ok := doc.Values["TASK_OWNER"]; ok {
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
