package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWriteReadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	doc := NewDocument()
	doc.Values = map[string]string{
		"FEISHU_APP_ID":     "cli_x",
		"FEISHU_APP_SECRET": `pa$s"word\x`,
		"FEISHU_AUTH_MODE":  "user",
		"ROOM_LIST":         "会议室A,会议室B",
		"ROOM_SIZE":         "15",
		"ROOM_TLS_INSECURE": "0",
		"SENTRY_DSN":        "", // 显式空必须往返保留
	}

	if err := WriteFile(path, doc); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Values, doc.Values) {
		t.Errorf("往返不一致:\n got %#v\nwant %#v", got.Values, doc.Values)
	}
}

func TestWriteFilePermAndDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "config.toml")
	if err := WriteFile(path, NewDocument()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("文件权限 = %o, want 0600", perm)
	}
}

func TestRenderPlaceholdersAndComments(t *testing.T) {
	out, err := Render(NewDocument())
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"room config",           // 文件头管理声明
		"[feishu]",              // 分节
		"# app_id = ",           // 未设置项为注释占位
		"# room_size = 0",       // int 默认占位
		"# tls_insecure = true", // bool 默认占位
		"飞书应用 ID",               // Desc 注释
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Render 输出缺少 %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "\napp_id = ") {
		t.Error("空文档不应输出未注释的赋值行")
	}
}

func TestUnknownKeysPreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	src := `top_extra = "keep"

[feishu]
app_id = "cli_x"
custom_key = 42

[mystuff]
a = "b"

[mystuff.nested]
c = "d"
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	doc, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Values["FEISHU_APP_ID"] != "cli_x" {
		t.Errorf("已知键解析失败: %v", doc.Values)
	}
	if doc.Extra[""]["top_extra"] != "keep" {
		t.Errorf("顶层未知键丢失: %v", doc.Extra)
	}
	if doc.Extra["feishu"]["custom_key"] != int64(42) {
		t.Errorf("已知节内未知键丢失: %v", doc.Extra["feishu"])
	}

	// 写回后未知项仍在,且文件可再次解析
	if err := WriteFile(path, doc); err != nil {
		t.Fatal(err)
	}
	again, err := ReadFile(path)
	if err != nil {
		t.Fatalf("重生成的文件应可解析: %v", err)
	}
	if again.Extra[""]["top_extra"] != "keep" || again.Extra["feishu"]["custom_key"] != int64(42) {
		t.Errorf("未知键写回丢失: %v", again.Extra)
	}
	if _, ok := again.Extra["mystuff"]; !ok {
		t.Errorf("未知整节丢失: %v", again.Extra)
	}
}

func TestRenderRejectsNestedTableInKnownSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	src := "[feishu]\napp_id = \"x\"\n\n[feishu.weird]\na = 1\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render(doc); err == nil {
		t.Error("已知节内的嵌套表应拒绝写回,而非静默丢弃")
	}
}

func TestReadFileMissing(t *testing.T) {
	doc, err := ReadFile(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("文件不存在应返回空文档: %v", err)
	}
	if len(doc.Values) != 0 {
		t.Errorf("空文档 Values 应为空: %v", doc.Values)
	}
}

func TestReadFileErrors(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		src  string
	}{
		{"corrupt", "not toml [[["},
		{"type mismatch", "[booking]\nroom_size = \"abc\"\n"},
		{"list item with comma", "[booking]\nroom_list = [\"A,B\"]\n"},
	}
	for _, tt := range tests {
		path := filepath.Join(dir, tt.name+".toml")
		if err := os.WriteFile(path, []byte(tt.src), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadFile(path); err == nil {
			t.Errorf("%s: 应报错", tt.name)
		}
	}
}

// 手写成逗号分隔字符串的 list 宽容接受。
func TestReadFileListAsString(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[booking]\nroom_list = \"A,B\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Values["ROOM_LIST"] != "A,B" {
		t.Errorf("ROOM_LIST = %q, want A,B", doc.Values["ROOM_LIST"])
	}
}
