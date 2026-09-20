package config

import (
	"fmt"
	"os"

	"github.com/amzyang/room/envutil"
)

// Source 配置项生效值的来源层。
type Source int

const (
	SourceUnset Source = iota
	SourceDefault
	SourceTOML
	SourceShellEnv
)

func (s Source) String() string {
	switch s {
	case SourceShellEnv:
		return "shell env"
	case SourceTOML:
		return "config.toml"
	case SourceDefault:
		return "默认"
	}
	return "未设置"
}

// Entry 一个配置项的生效值与来源。
type Entry struct {
	Value  string
	Source Source
}

// Resolved Bootstrap 的产物:生效配置总表,供 config 子命令展示与 override 告警。
type Resolved struct {
	Path    string           // 生效的 config.toml 路径
	Entries map[string]Entry // env key → 生效值(CleanEnvValue 后)与来源
	Warning string           // TOML 层加载失败等非致命告警
}

// OverrideOf 该 key 的生效来源若高于 TOML 层(shell env)则返回之,否则 SourceUnset。
// 用于「写入 TOML 后提醒用户当前环境仍会覆盖」。
func (r *Resolved) OverrideOf(envKey string) Source {
	if e, ok := r.Entries[envKey]; ok && e.Source == SourceShellEnv {
		return e.Source
	}
	return SourceUnset
}

// resolve 纯函数核心:两层原始值(map 中存在即显式设置)→ 生效表 + 待注入进程 env 的 TOML 值。
// 默认值只进生效表、绝不注入:注入 SENTRY_DSN 的默认空值会把「未设置」变成「显式设空」,
// 杀掉 sentry.go 的编译内置 fallback。
func resolve(shell, tomlVals map[string]string) (map[string]Entry, map[string]string) {
	entries := make(map[string]Entry, len(Registry))
	inject := map[string]string{}
	for _, it := range Registry {
		if v, ok := shell[it.EnvKey]; ok {
			entries[it.EnvKey] = Entry{envutil.CleanEnvValue(v), SourceShellEnv}
			continue
		}
		if v, ok := tomlVals[it.EnvKey]; ok {
			entries[it.EnvKey] = Entry{envutil.CleanEnvValue(v), SourceTOML}
			inject[it.EnvKey] = v
			continue
		}
		if it.Default != "" {
			entries[it.EnvKey] = Entry{it.Default, SourceDefault}
			continue
		}
		entries[it.EnvKey] = Entry{"", SourceUnset}
	}
	return entries, inject
}

// Bootstrap 命令式外壳,进程启动时调用一次:
// 快照 shell env → 读全局 TOML →
// 把仅在 TOML 层生效的值注入进程 env,使现有 os.Getenv 调用点零改动。
func Bootstrap(tomlPath string) *Resolved {
	shell := map[string]string{}
	for _, it := range Registry {
		if v, ok := os.LookupEnv(it.EnvKey); ok {
			shell[it.EnvKey] = v
		}
	}

	r := &Resolved{Path: tomlPath}
	tomlVals := map[string]string{}
	if doc, err := ReadFile(tomlPath); err != nil {
		// 不致命:损坏的 config.toml 不能把修复它所需的 room config 命令一起锁死
		r.Warning = fmt.Sprintf("配置文件 %s 解析失败,本次运行忽略该文件: %v", tomlPath, err)
	} else {
		tomlVals = doc.Values
	}

	entries, inject := resolve(shell, tomlVals)
	for k, v := range inject {
		_ = os.Setenv(k, v)
	}
	r.Entries = entries
	return r
}
