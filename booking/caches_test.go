package booking

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Node 版写出的各缓存文件应能被 Go 版读出；Go 版写出的结构与 Node 版一致。

func TestHolidayCacheNodeCompat(t *testing.T) {
	dir := t.TempDir()
	nodeWritten := `{
  "code": 200,
  "msg": "success",
  "result": {
    "update": false,
    "list": [
      {
        "holiday": "2026-01-01",
        "name": "元旦",
        "vacation": "2026-01-01|2026-01-02|2026-01-03",
        "remark": "",
        "wage": "3",
        "start": 0,
        "now": 0,
        "end": 0,
        "tip": "",
        "rest": ""
      }
    ]
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "2026.json"), []byte(nodeWritten), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := &HolidayCache{Dir: dir}
	resp := cache.Get("2026")
	if resp == nil || resp.Code != 200 {
		t.Fatalf("failed to read node-written holiday cache: %+v", resp)
	}
	if resp.Result.List[0].Vacation != "2026-01-01|2026-01-02|2026-01-03" {
		t.Errorf("vacation = %q", resp.Result.List[0].Vacation)
	}

	if err := cache.Save("2027", resp); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "2027.json"))
	if !strings.Contains(string(data), `"vacation": "2026-01-01|2026-01-02|2026-01-03"`) {
		t.Errorf("saved holiday cache missing vacation field:\n%s", data)
	}
}

func TestUserIDCacheNodeCompat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user_ids_lark.json")
	if err := os.WriteFile(path, []byte("{\n  \"zouyang\": \"10000027\"\n}"), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := &UserIDCache{Path: path}
	cache.Load()
	if cache.Get("zouyang") != "10000027" {
		t.Error("failed to read node-written user id cache")
	}
	if cache.Get("missing") != "" {
		t.Error("missing user should return empty")
	}

	if err := cache.Set("shikai", "10003329"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var parsed map[string]string
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["zouyang"] != "10000027" || parsed["shikai"] != "10003329" {
		t.Errorf("saved cache = %v", parsed)
	}
}

func TestUserIDCacheMissingFile(t *testing.T) {
	cache := &UserIDCache{Path: filepath.Join(t.TempDir(), "nope.json")}
	cache.Load()
	if cache.Get("any") != "" {
		t.Error("missing file should behave as empty cache")
	}
}

func TestAutoBookingCacheNodeCompat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auto-booking-cache.json")
	if err := os.WriteFile(path, []byte(`{"eventIds": ["evt-a_0", "evt-b_0"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := &AutoBookingCache{Path: path}
	cache.Load()
	if !cache.Has("evt-a_0") || !cache.Has("evt-b_0") || cache.Has("evt-c_0") {
		t.Error("failed to read node-written auto booking cache")
	}

	cache.Add("evt-c_0")
	cache.Add("evt-a_0") // 重复添加不产生重复项
	if err := cache.Save(); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	var parsed struct {
		EventIDs []string `json:"eventIds"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	want := []string{"evt-a_0", "evt-b_0", "evt-c_0"}
	if len(parsed.EventIDs) != len(want) {
		t.Fatalf("eventIds = %v, want %v", parsed.EventIDs, want)
	}
	for i := range want {
		if parsed.EventIDs[i] != want[i] {
			t.Errorf("eventIds[%d] = %q, want %q (insertion order)", i, parsed.EventIDs[i], want[i])
		}
	}
}

func TestAutoBookingCacheEmptySave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auto-booking-cache.json")
	cache := &AutoBookingCache{Path: path}
	cache.Load()
	if err := cache.Save(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"eventIds": []`) {
		t.Errorf("empty cache should serialize eventIds as [], got:\n%s", data)
	}
}
