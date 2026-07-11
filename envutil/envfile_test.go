package envutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joho/godotenv"
)

func writeEnvFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestUpsertEnvFileCreatesFileWith0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	err := UpsertEnvFile(path, []EnvPair{
		{Key: "FEISHU_APP_ID", Value: "cli_new123"},
		{Key: "FEISHU_APP_SECRET", Value: "newsecret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "FEISHU_APP_ID=cli_new123\nFEISHU_APP_SECRET=newsecret\n"
	if got := readFile(t, path); got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 0600", perm)
	}
}

func TestUpsertEnvFileUpdatesInPlace(t *testing.T) {
	pairs := []EnvPair{
		{Key: "FEISHU_APP_ID", Value: "cli_new"},
		{Key: "FEISHU_APP_SECRET", Value: "newsecret"},
	}
	tests := []struct {
		name   string
		before string
		pairs  []EnvPair
		want   string
	}{
		{
			name:   "替换并逐字节保留注释、空行与顺序",
			before: "# ============ 飞书应用（必填） ============\nFEISHU_APP_ID=cli_old\nFEISHU_APP_SECRET=oldsecret\n\nTASK_OWNER=zhangsan\n",
			pairs:  pairs,
			want:   "# ============ 飞书应用（必填） ============\nFEISHU_APP_ID=cli_new\nFEISHU_APP_SECRET=newsecret\n\nTASK_OWNER=zhangsan\n",
		},
		{
			name:   "export 前缀与等号两侧空格的变体",
			before: "export FEISHU_APP_ID=cli_old\nFEISHU_APP_SECRET = oldsecret\n",
			pairs:  pairs,
			want:   "FEISHU_APP_ID=cli_new\nFEISHU_APP_SECRET=newsecret\n",
		},
		{
			name:   "重复 key 的所有行都被替换",
			before: "FEISHU_APP_ID=a\nTASK_OWNER=li\nFEISHU_APP_ID=b\n",
			pairs:  []EnvPair{{Key: "FEISHU_APP_ID", Value: "cli_new"}},
			want:   "FEISHU_APP_ID=cli_new\nTASK_OWNER=li\nFEISHU_APP_ID=cli_new\n",
		},
		{
			name:   "注释掉的同名行不动，缺失时追加",
			before: "# FEISHU_APP_ID=cli_disabled\nTASK_OWNER=li\n",
			pairs:  []EnvPair{{Key: "FEISHU_APP_ID", Value: "cli_new"}},
			want:   "# FEISHU_APP_ID=cli_disabled\nTASK_OWNER=li\nFEISHU_APP_ID=cli_new\n",
		},
		{
			name:   "原文件无结尾换行时先补换行再追加",
			before: "TASK_OWNER=li",
			pairs:  []EnvPair{{Key: "FEISHU_APP_ID", Value: "cli_new"}},
			want:   "TASK_OWNER=li\nFEISHU_APP_ID=cli_new\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeEnvFile(t, tt.before, 0o600)
			if err := UpsertEnvFile(path, tt.pairs); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, path); got != tt.want {
				t.Errorf("content = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUpsertEnvFileKeepsExistingMode(t *testing.T) {
	path := writeEnvFile(t, "FEISHU_APP_ID=old\n", 0o644)
	if err := UpsertEnvFile(path, []EnvPair{{Key: "FEISHU_APP_ID", Value: "cli_new"}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("mode = %o, want 0644（保留原权限）", perm)
	}
}

func TestUpsertEnvFileQuotingRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		wantLine string
	}{
		{name: "飞书凭证类值裸写", value: "cli_a1B2.c-3:d/e@f+_", wantLine: "KEY=cli_a1B2.c-3:d/e@f+_\n"},
		{name: "含空格加双引号", value: "a b", wantLine: "KEY=\"a b\"\n"},
		{name: "含引号与反斜杠转义", value: `a"b\c`, wantLine: "KEY=\"a\\\"b\\\\c\"\n"},
		{name: "含 $ 转义防 godotenv 变量展开", value: "ab$CDE9", wantLine: "KEY=\"ab\\$CDE9\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			if err := UpsertEnvFile(path, []EnvPair{{Key: "KEY", Value: tt.value}}); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, path); got != tt.wantLine {
				t.Errorf("content = %q, want %q", got, tt.wantLine)
			}
			vals, err := godotenv.Read(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := CleanEnvValue(vals["KEY"]); got != tt.value {
				t.Errorf("godotenv 往返 = %q, want %q", got, tt.value)
			}
		})
	}
}
