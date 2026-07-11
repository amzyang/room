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
	FieldConfirm
)

// FieldSpec 一个表单字段的中间表示。
type FieldSpec struct {
	Item     Item
	Kind     FieldKind
	Title    string
	Desc     string
	Masked   bool     // secret 项掩码输入
	Options  []string // 仅 FieldSelect
	Initial  string   // 生效值预填(bool 为 "1"/"0")
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
	{"通知与其他", []string{"notify", "sentry", "network"}},
}

// BuildFormSpec 纯函数:schema + 生效值 → 表单中间表示。
func BuildFormSpec(effective map[string]string) []GroupSpec {
	var out []GroupSpec
	for _, g := range formGroups {
		spec := GroupSpec{Title: g.title}
		for _, section := range g.sections {
			for _, it := range Registry {
				if it.Section == section {
					spec.Fields = append(spec.Fields, buildField(it, effective[it.EnvKey]))
				}
			}
		}
		out = append(out, spec)
	}
	return out
}

func buildField(it Item, initial string) FieldSpec {
	f := FieldSpec{
		Item:    it,
		Kind:    FieldInput,
		Title:   it.TOMLKey(),
		Desc:    it.Desc,
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
	case it.Type == TypeEnum:
		f.Kind = FieldSelect
		f.Options = it.Enum
		if initial == "" {
			f.Initial = it.Default
		}
	case it.Type == TypeBool:
		f.Kind = FieldConfirm
		if initial == "" {
			f.Initial = it.Default
		}
	case it.Multiline:
		f.Kind = FieldText
	}
	return f
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
