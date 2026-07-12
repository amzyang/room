package config

import (
	"fmt"
	"strings"
)

// FieldKind TUI 控件类型。FieldSpec/GroupSpec 是不含 TUI 库类型的中间表示,
// 表单结构与校验行为在此层纯函数化;huh 适配器只做机械翻译,不进测试。
type FieldKind int

const (
	FieldInput FieldKind = iota
	FieldText
	FieldSelect
)

// Option Select 控件的一个选项;enum 场景 label==value,层级场景显示路径、写入 ID。
type Option struct {
	Label string
	Value string
}

// FieldSpec 一个表单字段的中间表示。
type FieldSpec struct {
	Item     Item
	Kind     FieldKind
	Title    string   // 单行「文案[（必填）]  键名」
	Masked   bool     // secret 项掩码输入
	Options  []Option // 仅 FieldSelect
	Initial  string   // 生效值预填
	Validate func(string) error
}

// GroupSpec 一组表单字段。
type GroupSpec struct {
	Title  string
	Fields []FieldSpec
}

// formGroups TUI 分组:标题 → 包含的 TOML 节(顺序即表单顺序)。
var formGroups = []struct {
	title    string
	sections []string
}{
	{"飞书", []string{"feishu"}},
	{"预订", []string{"booking"}},
	{"NLP(book 命令自然语言解析)", []string{"nlp"}},
	// sentry.dsn 刻意不进 TUI(经 room config set 管理),避免误触改动错误上报
	{"通知", []string{"notify"}},
}

// BuildFormSpec 纯函数:schema + 生效值 + 会议室层级选项(可为 nil) → 表单中间表示。
// levels 非空时 room_level_id 渲染为层级下拉,否则保持文本输入(凭证缺失/拉取失败的降级)。
func BuildFormSpec(effective map[string]string, levels []Option) []GroupSpec {
	var out []GroupSpec
	for _, g := range formGroups {
		spec := GroupSpec{Title: g.title}
		for _, section := range g.sections {
			for _, it := range Registry {
				if it.Section == section {
					spec.Fields = append(spec.Fields, buildField(it, effective[it.EnvKey], levels))
				}
			}
		}
		out = append(out, spec)
	}
	return out
}

func buildField(it Item, initial string, levels []Option) FieldSpec {
	title := it.Desc
	if it.Required {
		title += "（必填）"
	}
	f := FieldSpec{
		Item:    it,
		Kind:    FieldInput,
		Title:   title + "  " + it.TOMLKey(),
		Masked:  it.Secret,
		Initial: initial,
		Validate: func(s string) error {
			if strings.TrimSpace(s) == "" {
				if it.Required {
					return fmt.Errorf("必填项")
				}
				return nil
			}
			return it.Validate(s)
		},
	}
	switch {
	case it.EnvKey == "ROOM_LEVEL_ID" && len(levels) > 0:
		f.Kind = FieldSelect
		f.Options = levelOptions(levels, initial)
	case it.Type == TypeEnum:
		f.Kind = FieldSelect
		for _, e := range it.Enum {
			f.Options = append(f.Options, Option{Label: e, Value: e})
		}
		if initial == "" {
			f.Initial = it.Default
		}
	case it.Multiline:
		f.Kind = FieldText
	}
	return f
}

// levelOptions 前置「（不限）」空值项;生效值不在树中(层级已删除)时追加「(当前值)」项,
// 否则 huh Select 会在用户直接走过表单时把配置静默改成首个选项。
func levelOptions(levels []Option, current string) []Option {
	out := append([]Option{{Label: "（不限）"}}, levels...)
	for _, o := range out {
		if o.Value == current {
			return out
		}
	}
	return append(out, Option{Label: current + "（当前值）", Value: current})
}

// ApplyFormResult 表单结果合并进文档:非空值规范化写入(等于默认值也写,固化用户显式确认的值);
// 空值 = 取消设置。TUI 表达不了「显式空 vs 未设置」,原本的显式空保留(禁用 Sentry 请用
// room config set sentry.dsn "")。
func ApplyFormResult(doc *Document, results map[string]string) error {
	for k, raw := range results {
		it, ok := ByEnvKey(k)
		if !ok {
			continue
		}
		if strings.TrimSpace(raw) == "" {
			if old, exists := doc.Values[k]; exists && old == "" {
				continue // 保留显式空
			}
			delete(doc.Values, k)
			continue
		}
		v, err := it.Normalize(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", it.TOMLKey(), err)
		}
		doc.Values[k] = v
	}
	return nil
}
