// Package envutil 统一清洗环境变量值：去除首尾空格与引号（对齐原 Node 版 env-utils.ts）。
package envutil

import (
	"strconv"
	"strings"
)

// CleanEnvValue 去除首尾空格和单/双引号；引号内的首尾空格也一并去除。
func CleanEnvValue(value string) string {
	cleaned := strings.TrimSpace(value)
	cleaned = strings.TrimPrefix(cleaned, `"`)
	cleaned = strings.TrimPrefix(cleaned, `'`)
	cleaned = strings.TrimSuffix(cleaned, `"`)
	cleaned = strings.TrimSuffix(cleaned, `'`)
	return strings.TrimSpace(cleaned)
}

// ParseEnvList 解析逗号分隔的列表，清洗每一项并过滤空项。
func ParseEnvList(value string) []string {
	cleaned := CleanEnvValue(value)
	if cleaned == "" {
		return nil
	}
	var items []string
	for _, item := range strings.Split(cleaned, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

// ParseEnvInt 解析整数环境变量，空值或非法值返回默认值。
func ParseEnvInt(value string, defaultValue int) int {
	cleaned := CleanEnvValue(value)
	if cleaned == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(cleaned)
	if err != nil {
		return defaultValue
	}
	return parsed
}
