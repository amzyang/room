// Package config 集中管理 room 的配置 schema 与分层加载。
// 分层优先级:shell 环境变量 > 全局 config.toml > 内置默认。
//
// 约束:所有配置消费必须发生在 Bootstrap 之后并经 os.Getenv/os.LookupEnv 读取;
// 不要在包级 var 或 init() 中读环境变量,否则会读到 TOML 注入前的值。
package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/amzyang/room/envutil"
	"github.com/amzyang/room/nlp"
)

// 与 main 包共用的默认值,单一事实来源。
const DefaultUserTokenPath = ".cache/feishu-user-token.json"

// Type 配置项的值类型,决定校验规则与 TOML 表示。
type Type int

const (
	TypeString Type = iota
	TypeInt
	TypeList // env 编码逗号分隔;TOML 为字符串数组
	TypeEnum
)

// Item 一个配置项的元数据,驱动 set 校验、TUI 表单、list 展示与 TOML 读写。
type Item struct {
	EnvKey    string // 环境变量名,与现有 env() 调用点一一对应,不可改名
	Section   string // TOML 节名
	Key       string // TOML 键名
	Type      Type
	Default   string // 规范 env 字符串形态("" = 无默认)
	Required  bool
	Secret    bool
	Multiline bool     // 长值,TUI 用多行控件
	Enum      []string // 仅 TypeEnum
	Desc      string   // 一行描述:TOML 注释、TUI 描述、list 展示共用
}

// TOMLKey 点分形式,如 feishu.app_id。
func (it Item) TOMLKey() string { return it.Section + "." + it.Key }

// Registry 全部配置项,有序:即 config.toml 生成顺序与 TUI 表单顺序。
var Registry = []Item{
	{EnvKey: "FEISHU_APP_ID", Section: "feishu", Key: "app_id", Type: TypeString, Required: true,
		Desc: "飞书应用 ID(可运行 room init 自动创建)"},
	{EnvKey: "FEISHU_APP_SECRET", Section: "feishu", Key: "app_secret", Type: TypeString, Required: true, Secret: true,
		Desc: "飞书应用密钥"},
	{EnvKey: "FEISHU_AUTH_MODE", Section: "feishu", Key: "auth_mode", Type: TypeEnum, Default: "auto", Enum: []string{"auto", "user", "tenant"},
		Desc: "鉴权模式:auto(用户优先、应用兜底)/ user / tenant"},
	{EnvKey: "FEISHU_USER_TOKEN_PATH", Section: "feishu", Key: "user_token_path", Type: TypeString, Default: DefaultUserTokenPath,
		Desc: "用户凭证存储路径(相对路径基于运行时所在目录)"},
	{EnvKey: "TASK_OWNER", Section: "booking", Key: "task_owner", Type: TypeString, Required: true,
		Desc: "任务负责人邮箱前缀(自动加入所有会议参与者)"},
	{EnvKey: "ROOM_LIST", Section: "booking", Key: "room_list", Type: TypeList, Required: true,
		Desc: "会议室优先级列表"},
	{EnvKey: "ROOM_EXCLUDE_LIST", Section: "booking", Key: "room_exclude_list", Type: TypeList,
		Desc: "排除包含以下关键词的会议室"},
	{EnvKey: "ROOM_LEVEL_ID", Section: "booking", Key: "room_level_id", Type: TypeString,
		Desc: "会议室层级 ID(配置后按层级+楼层顺序查找会议室;room config set booking.room_level_id 可交互选择)"},
	{EnvKey: "ROOM_SIZE", Section: "booking", Key: "room_size", Type: TypeInt, Default: "0",
		Desc: "会议室最大容量限制(0 为不限制)"},
	{EnvKey: "TASK_FORMAT", Section: "booking", Key: "task_format", Type: TypeString, Multiline: true,
		Desc: "自动预订任务 DSL(多任务用 | 分隔,语法见 README)"},
	{EnvKey: "EMAIL_DOMAIN", Section: "booking", Key: "email_domain", Type: TypeString,
		Desc: "邮箱域名(参与者邮箱前缀 → 完整邮箱)"},
	{EnvKey: "TIANAPI_KEY", Section: "booking", Key: "tianapi_key", Type: TypeString, Secret: true,
		Desc: "tianapi 节假日 API key(不设则跳过节假日过滤)"},
	{EnvKey: "FEISHU_BOT_WEBHOOK", Section: "notify", Key: "webhook", Type: TypeString,
		Desc: "自定义机器人 webhook 地址(notify 命令)"},
	{EnvKey: "FEISHU_BOT_WEBHOOK_SECRET", Section: "notify", Key: "webhook_secret", Type: TypeString, Secret: true,
		Desc: "webhook 签名密钥"},
	{EnvKey: "OPENAI_API_KEY", Section: "nlp", Key: "api_key", Type: TypeString, Secret: true,
		Desc: "OpenAI 兼容 API key(book 命令自然语言解析)"},
	{EnvKey: "OPENAI_API_BASE", Section: "nlp", Key: "api_base", Type: TypeString, Default: nlp.DefaultAPIBase,
		Desc: "OpenAI 兼容 API 地址"},
	{EnvKey: "MODEL", Section: "nlp", Key: "model", Type: TypeString, Default: nlp.DefaultModel,
		Desc: "自然语言解析使用的模型"},
	{EnvKey: "SENTRY_DSN", Section: "sentry", Key: "dsn", Type: TypeString, Secret: true,
		Desc: "Sentry 错误上报 DSN(显式设空则禁用;未设置时 release 二进制回退编译内置)"},
}

// Sections 各节在 Registry 中的首现顺序。
func Sections() []string {
	var out []string
	seen := map[string]bool{}
	for _, it := range Registry {
		if !seen[it.Section] {
			seen[it.Section] = true
			out = append(out, it.Section)
		}
	}
	return out
}

// ByEnvKey 按环境变量名查找,大小写不敏感。
func ByEnvKey(key string) (Item, bool) {
	for _, it := range Registry {
		if strings.EqualFold(it.EnvKey, key) {
			return it, true
		}
	}
	return Item{}, false
}

// ByTOMLKey 按点分 TOML key 查找,大小写不敏感。
func ByTOMLKey(key string) (Item, bool) {
	for _, it := range Registry {
		if strings.EqualFold(it.TOMLKey(), key) {
			return it, true
		}
	}
	return Item{}, false
}

// Lookup 解析用户输入的 KEY:含 "." 视为 TOML key,否则视为 env 名。
// 未知 key 报错并附近似候选。
func Lookup(arg string) (Item, error) {
	key := strings.TrimSpace(arg)
	if strings.Contains(key, ".") {
		if it, ok := ByTOMLKey(key); ok {
			return it, nil
		}
	} else if it, ok := ByEnvKey(key); ok {
		return it, nil
	}

	lower := strings.ToLower(key)
	var hints []string
	for _, it := range Registry {
		if strings.Contains(strings.ToLower(it.EnvKey), lower) || strings.Contains(it.TOMLKey(), lower) {
			hints = append(hints, it.TOMLKey())
		}
	}
	if len(hints) > 0 {
		return Item{}, fmt.Errorf("未知配置项 %q,是否想找:%s", arg, strings.Join(hints, "、"))
	}
	return Item{}, fmt.Errorf("未知配置项 %q(用 room config list 查看全部)", arg)
}

// Normalize 校验并规范化用户输入为 env 字符串形态(list 逗号连接、enum 小写)。
// 空值:string 允许(显式空,如禁用 Sentry)、list 表示空列表;int/enum 拒绝(取消设置请用 unset)。
func (it Item) Normalize(raw string) (string, error) {
	v := envutil.CleanEnvValue(raw)
	switch it.Type {
	case TypeInt:
		if v == "" {
			return "", fmt.Errorf("%s 需要整数值(取消设置请用 room config unset)", it.TOMLKey())
		}
		if _, err := strconv.Atoi(v); err != nil {
			return "", fmt.Errorf("%s 需要整数值,收到 %q", it.TOMLKey(), raw)
		}
		return v, nil
	case TypeEnum:
		for _, e := range it.Enum {
			if strings.EqualFold(v, e) {
				return e, nil
			}
		}
		return "", fmt.Errorf("%s 只接受 %s,收到 %q", it.TOMLKey(), strings.Join(it.Enum, "/"), raw)
	case TypeList:
		if v == "" {
			return "", nil
		}
		items := envutil.ParseEnvList(v)
		if len(items) == 0 {
			return "", fmt.Errorf("%s 列表为空(多项用逗号分隔)", it.TOMLKey())
		}
		return strings.Join(items, ","), nil
	}
	return v, nil
}

// Validate 校验用户输入(丢弃规范化结果)。
func (it Item) Validate(v string) error {
	_, err := it.Normalize(v)
	return err
}
