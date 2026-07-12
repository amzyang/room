package booking

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// 缓存文件均与 Node 版 JSON 格式互换（两缩进空格），路径由调用方注入（配置目录旁的 cache/）。

// HolidayResponse tianapi 节假日接口响应；缓存时保留原始结构。
type HolidayResponse struct {
	Code   int    `json:"code"`
	Msg    string `json:"msg"`
	Result struct {
		Update bool      `json:"update"`
		List   []Holiday `json:"list"`
	} `json:"result"`
}

// Holiday 单个节假日；Vacation 为 | 分隔的放假日期列表。
type Holiday struct {
	Holiday  string `json:"holiday"`
	Name     string `json:"name"`
	Vacation string `json:"vacation"`
	Remark   string `json:"remark"`
	Wage     string `json:"wage"`
	Start    int64  `json:"start"`
	Now      int64  `json:"now"`
	End      int64  `json:"end"`
	Tip      string `json:"tip"`
	Rest     string `json:"rest"`
}

// HolidayCache 按年份缓存 tianapi 原始响应到 Dir 下的 <year>.json。
type HolidayCache struct {
	Dir string
}

func (c *HolidayCache) Get(year string) *HolidayResponse {
	data, err := os.ReadFile(filepath.Join(c.Dir, year+".json"))
	if err != nil {
		return nil
	}
	var resp HolidayResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil
	}
	return &resp
}

func (c *HolidayCache) Save(year string, resp *HolidayResponse) error {
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.Dir, year+".json"), data, 0o644)
}

// UserIDCache email 前缀 → 飞书 user_id 的持久化映射（user_ids_lark.json）。
type UserIDCache struct {
	Path  string
	cache map[string]string
}

func (c *UserIDCache) Load() {
	c.cache = map[string]string{}
	data, err := os.ReadFile(c.Path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &c.cache)
}

func (c *UserIDCache) Get(username string) string {
	return c.cache[username]
}

func (c *UserIDCache) Set(username, userID string) error {
	c.cache[username] = userID
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c.cache, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.Path, data, 0o644)
}

// AutoBookingCache 本工具创建过的事件 ID 集合（auto-booking-cache.json）。
// 已取消但由本工具创建的事件仍视为时间段占用，避免重复预订。
type AutoBookingCache struct {
	Path     string
	eventIDs []string
	seen     map[string]bool
}

func (c *AutoBookingCache) Load() {
	c.seen = map[string]bool{}
	c.eventIDs = nil
	data, err := os.ReadFile(c.Path)
	if err != nil {
		return
	}
	var parsed struct {
		EventIDs []string `json:"eventIds"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return
	}
	for _, id := range parsed.EventIDs {
		if !c.seen[id] {
			c.seen[id] = true
			c.eventIDs = append(c.eventIDs, id)
		}
	}
}

func (c *AutoBookingCache) Has(eventID string) bool {
	return c.seen[eventID]
}

func (c *AutoBookingCache) Add(eventID string) {
	if c.seen[eventID] {
		return
	}
	c.seen[eventID] = true
	c.eventIDs = append(c.eventIDs, eventID)
}

func (c *AutoBookingCache) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
		return err
	}
	payload := struct {
		EventIDs []string `json:"eventIds"`
	}{EventIDs: c.eventIDs}
	if payload.EventIDs == nil {
		payload.EventIDs = []string{}
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.Path, data, 0o644)
}
