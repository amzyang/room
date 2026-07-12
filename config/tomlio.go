package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/amzyang/room/envutil"
)

// Document config.toml 的解析结果。
type Document struct {
	Values map[string]string         // env key → 规范 env 字符串形态(含显式空值)
	Extra  map[string]map[string]any // schema 之外的键,按节保留("" 为顶层散键)
}

// NewDocument 空文档。
func NewDocument() *Document {
	return &Document{Values: map[string]string{}, Extra: map[string]map[string]any{}}
}

// ReadFile 解析 config.toml。文件不存在返回空文档;解析/类型错误返回 error(调用方决定降级或中止)。
func ReadFile(path string) (*Document, error) {
	doc := NewDocument()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return doc, nil
	}
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	for k, v := range raw {
		sub, ok := v.(map[string]any)
		if !ok {
			doc.addExtra("", k, v)
			continue
		}
		for kk, vv := range sub {
			it, found := ByTOMLKey(k + "." + kk)
			if !found {
				doc.addExtra(k, kk, vv)
				continue
			}
			val, err := canonicalFromTOML(it, vv)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", k, kk, err)
			}
			doc.Values[it.EnvKey] = val
		}
	}
	return doc, nil
}

func (d *Document) addExtra(section, key string, v any) {
	if d.Extra[section] == nil {
		d.Extra[section] = map[string]any{}
	}
	d.Extra[section][key] = v
}

// canonicalFromTOML TOML 原生类型 → 规范 env 字符串形态。
func canonicalFromTOML(it Item, v any) (string, error) {
	switch it.Type {
	case TypeInt:
		if n, ok := v.(int64); ok {
			return strconv.FormatInt(n, 10), nil
		}
	case TypeList:
		switch arr := v.(type) {
		case []any:
			items := make([]string, 0, len(arr))
			for _, e := range arr {
				s, ok := e.(string)
				if !ok {
					return "", fmt.Errorf("列表项必须是字符串")
				}
				s = strings.TrimSpace(s)
				if s == "" {
					continue
				}
				if strings.Contains(s, ",") {
					return "", fmt.Errorf("列表项 %q 不能包含逗号(环境变量层编码为逗号分隔)", s)
				}
				items = append(items, s)
			}
			return strings.Join(items, ","), nil
		case string:
			return arr, nil // 宽容:手写成逗号分隔字符串也接受
		}
	default: // TypeString / TypeEnum
		if s, ok := v.(string); ok {
			return s, nil
		}
	}
	return "", fmt.Errorf("类型不符(期望%s)", typeName(it.Type))
}

func typeName(t Type) string {
	switch t {
	case TypeInt:
		return "整数"
	case TypeList:
		return "字符串数组"
	}
	return "字符串"
}

const fileHeader = `# 此文件由 room config 管理;手工添加的注释会在下次保存时丢失。
# 优先级:shell 环境变量 > 此文件 > 内置默认。
`

// Render schema 驱动整文件重生成:值来自 doc、注释来自 schema,未识别项原样保留。
// 已设置项输出赋值行(含显式空),未设置项输出「# key = 默认值」注释占位。
func Render(doc *Document) ([]byte, error) {
	var b strings.Builder
	b.WriteString(fileHeader)

	// TOML 顶层散键必须出现在任何节之前
	if extra := doc.Extra[""]; len(extra) > 0 {
		b.WriteString("\n# --- 未识别项(原样保留) ---\n")
		if err := renderExtraKVs(&b, extra); err != nil {
			return nil, err
		}
	}

	for _, section := range Sections() {
		b.WriteString("\n[" + section + "]\n")
		for _, it := range Registry {
			if it.Section != section {
				continue
			}
			b.WriteString("# " + it.Desc + "\n")
			v, ok := doc.Values[it.EnvKey]
			if !ok {
				b.WriteString("# " + it.Key + " = " + placeholderLiteral(it) + "\n")
				continue
			}
			lit, err := valueLiteral(it, v)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", it.TOMLKey(), err)
			}
			b.WriteString(it.Key + " = " + lit + "\n")
		}
		if extra := doc.Extra[section]; len(extra) > 0 {
			b.WriteString("# --- 未识别项(原样保留) ---\n")
			if err := renderExtraKVs(&b, extra); err != nil {
				return nil, fmt.Errorf("[%s] %w", section, err)
			}
		}
	}

	// 未识别的整节(可含嵌套表)交给 toml.Marshal,节名不与 schema 冲突,追加安全
	known := map[string]bool{"": true}
	for _, s := range Sections() {
		known[s] = true
	}
	var extraSections []string
	for s := range doc.Extra {
		if !known[s] {
			extraSections = append(extraSections, s)
		}
	}
	sort.Strings(extraSections)
	for _, s := range extraSections {
		out, err := toml.Marshal(map[string]any{s: doc.Extra[s]})
		if err != nil {
			return nil, err
		}
		b.WriteString("\n# --- 未识别配置节(原样保留) ---\n")
		b.Write(out)
	}
	return []byte(b.String()), nil
}

// renderExtraKVs 未识别键的内联输出;嵌套表无法安全放回已知节,拒绝写(不静默丢弃)。
func renderExtraKVs(b *strings.Builder, kvs map[string]any) error {
	keys := make([]string, 0, len(kvs))
	for k := range kvs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, isTable := kvs[k].(map[string]any); isTable {
			return fmt.Errorf("包含无法归类的嵌套表 %q,请手动整理后重试", k)
		}
		lit, err := tomlLiteral(kvs[k])
		if err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
		b.WriteString(k + " = " + lit + "\n")
	}
	return nil
}

// tomlLiteral 借 toml.Marshal 生成单值字面量,避免手写转义规则。
func tomlLiteral(v any) (string, error) {
	out, err := toml.Marshal(map[string]any{"v": v})
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(out))
	if !strings.HasPrefix(s, "v = ") {
		return "", fmt.Errorf("无法内联表示的值")
	}
	return strings.TrimPrefix(s, "v = "), nil
}

func valueLiteral(it Item, v string) (string, error) {
	switch it.Type {
	case TypeInt:
		return v, nil // Normalize/canonicalFromTOML 已保证是数字
	case TypeList:
		return tomlLiteral(envutil.ParseEnvList(v))
	}
	return tomlLiteral(v)
}

func placeholderLiteral(it Item) string {
	if it.Default != "" {
		if lit, err := valueLiteral(it, it.Default); err == nil {
			return lit
		}
	}
	switch it.Type {
	case TypeInt:
		return "0"
	case TypeList:
		return "[]"
	}
	return `""`
}

// WriteFile 整文件重生成并原子写入;目录 0700、文件 0600(可能含凭证)。
func WriteFile(path string, doc *Document) error {
	data, err := Render(doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return envutil.WriteFileAtomic(path, data, 0o600)
}
