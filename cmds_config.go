package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/amzyang/room/booking"
	"github.com/amzyang/room/config"
	"github.com/amzyang/room/output"
)

// newConfigCmd config 命令族：查看与修改全局配置文件（默认 ~/.config/room/config.toml）。
func newConfigCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "查看与修改配置（全局 config.toml）",
		Long: "管理全局配置文件。KEY 接受 TOML 键（feishu.app_id）或环境变量名（FEISHU_APP_ID）。\n" +
			"优先级：shell 环境变量 > config.toml > 内置默认。\n" +
			"不带子命令且在终端中运行时，进入交互式表单编辑全部配置。\n" +
			"get/set/list/unset/path 均支持 --json 输出信封（secret 值在 set/list 中掩码）。",
		Example: `  room config list --json
  room config get feishu.app_id --json
  room config set booking.email_domain example.com
  room config set booking.room_level_id        # 终端下交互式层级选择
  room config tasks                            # 交互式管理自动预订任务`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// --json 下人类帮助文本会污染机读 stdout，fail-fast 指引子命令
			if a.jsonOut {
				return output.Errf(output.TypeValidation,
					"使用子命令：room config list/get/set/unset/path（均支持 --json）",
					"config 需要子命令")
			}
			if !a.interactive() {
				return cmd.Help()
			}
			return runConfigTUI(cmd, a)
		},
	}
	cmd.AddCommand(
		newConfigSetCmd(a),
		newConfigGetCmd(a),
		newConfigListCmd(a),
		newConfigUnsetCmd(a),
		newConfigPathCmd(a),
		newConfigTasksCmd(a),
	)
	return cmd
}

// newConfigTasksCmd booking.task_format 的可发现入口，与 set 省略 VALUE 进同一编辑器。
func newConfigTasksCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "tasks",
		Short: "交互式管理自动预订任务（booking.task_format）",
		Long: "任务列表菜单中新增/编辑/复制/删除自动预订任务，逐字段引导填写并在保存前\n" +
			"预览生成的 DSL 与接下来的命中日期。改动在「保存并退出」后写入 config.toml。",
		Args: validationArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfigTasksEditor(cmd, a)
		},
	}
}

// sourceSlug JSON 输出用的稳定来源标识（人类表格用 Source.String()）。
func sourceSlug(s config.Source) string {
	switch s {
	case config.SourceShellEnv:
		return "shell_env"
	case config.SourceTOML:
		return "config.toml"
	case config.SourceDefault:
		return "default"
	}
	return "unset"
}

// lookupItem 把未知 KEY 归入 validation（exit 2）。
func lookupItem(key string) (config.Item, error) {
	it, err := config.Lookup(key)
	if err != nil {
		return it, output.Wrap(output.TypeValidation, "运行 room config list 查看全部可用 KEY", err)
	}
	return it, nil
}

func newConfigSetCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "set KEY [VALUE]",
		Short: "写入一项配置到 config.toml（room_level_id/task_format 省略 VALUE 时交互编辑）",
		Args:  validationArgs(cobra.RangeArgs(1, 2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			it, err := lookupItem(args[0])
			if err != nil {
				return err
			}
			if len(args) == 1 {
				switch it.EnvKey {
				case "ROOM_LEVEL_ID":
					return runConfigPickRoomLevel(cmd, a)
				case "TASK_FORMAT":
					return runConfigTasksEditor(cmd, a)
				default:
					return output.Errf(output.TypeValidation,
						"补充 VALUE 参数（仅 booking.room_level_id / booking.task_format 支持省略 VALUE 交互编辑）",
						"set %s 缺少 VALUE", it.TOMLKey())
				}
			}
			val, err := setConfigValue(a, it, args[1])
			if err != nil {
				return err
			}
			display := val
			if it.Secret {
				display = maskSecret(val)
			}
			if a.jsonOut {
				data := configSetData{Key: it.TOMLKey(), Env: it.EnvKey, Value: display, Path: a.cfg.Path}
				if src := a.cfg.OverrideOf(it.EnvKey); src != config.SourceUnset {
					data.OverriddenBy = sourceSlug(src)
				}
				return output.WriteSuccess(cmd.OutOrStdout(), data, nil)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "已写入 %s: %s = %s\n", a.cfg.Path, it.TOMLKey(), display)
			warnOverride(cmd, a, it)
			return nil
		},
	}
}

// configSetData config set --json 的 data 载荷（secret 值已掩码）。
type configSetData struct {
	Key          string `json:"key"`
	Env          string `json:"env"`
	Value        string `json:"value"`
	Path         string `json:"path"`
	OverriddenBy string `json:"overridden_by,omitempty"` // 非空表示写入被更高层配置压住，当前不生效
}

// setConfigValue 规范化 raw 并写入 config.toml，返回写入的规范值。
// task_format 额外做 DSL 校验，否则写错的任务在 room auto 运行时静默不生效。
func setConfigValue(a *app, it config.Item, raw string) (string, error) {
	val, err := it.Normalize(raw)
	if err != nil {
		return "", err
	}
	if it.EnvKey == "TASK_FORMAT" {
		if err := booking.ValidateTaskFormat(val); err != nil {
			return "", err
		}
	}
	doc, err := config.ReadFile(a.cfg.Path)
	if err != nil {
		return "", fmt.Errorf("读取 %s 失败: %w", a.cfg.Path, err)
	}
	doc.Values[it.EnvKey] = val
	if err := config.WriteFile(a.cfg.Path, doc); err != nil {
		return "", err
	}
	return val, nil
}

// warnOverride 写入 TOML 后，若该 key 当前被更高层（shell env）覆盖则提醒，
// 否则用户会困惑「改了为什么不生效」。
func warnOverride(cmd *cobra.Command, a *app, it config.Item) {
	if src := a.cfg.OverrideOf(it.EnvKey); src != config.SourceUnset {
		fmt.Fprintf(cmd.ErrOrStderr(), "注意: %s 当前来自 %s，在该环境下本次写入不生效（请移除对应的 %s 设置）\n",
			it.TOMLKey(), src, it.EnvKey)
	}
}

func newConfigGetCmd(a *app) *cobra.Command {
	var showSource bool
	cmd := &cobra.Command{
		Use:   "get KEY",
		Short: "打印一项配置的生效值（不掩码，供脚本消费）",
		Args:  validationArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			it, err := lookupItem(args[0])
			if err != nil {
				return err
			}
			e := a.cfg.Entries[it.EnvKey]
			if a.jsonOut {
				return output.WriteSuccess(cmd.OutOrStdout(), struct {
					Key    string `json:"key"`
					Env    string `json:"env"`
					Value  string `json:"value"`
					Source string `json:"source"`
				}{it.TOMLKey(), it.EnvKey, e.Value, sourceSlug(e.Source)}, nil)
			}
			if showSource {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", e.Value, e.Source)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), e.Value)
			return nil
		},
	}
	cmd.Flags().BoolVar(&showSource, "source", false, "附加显示值来源（shell env/config.toml/默认）")
	return cmd
}

func newConfigListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "列出全部配置项的生效值与来源（secret 掩码）",
		Args:  validationArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, statErr := os.Stat(a.cfg.Path)
			if a.jsonOut {
				data := configListData(a.cfg, statErr == nil)
				return output.WriteSuccess(cmd.OutOrStdout(), data, &output.Meta{Count: len(data.Items)})
			}
			fmt.Fprint(cmd.OutOrStdout(), formatConfigList(a.cfg, statErr == nil))
			return nil
		},
	}
}

type configListItem struct {
	Key    string `json:"key"`
	Env    string `json:"env"`
	Value  string `json:"value"` // secret 项已掩码，完整值用 room config get
	Secret bool   `json:"secret"`
	Source string `json:"source"`
}

type configList struct {
	Path       string           `json:"path"`
	FileExists bool             `json:"file_exists"`
	Items      []configListItem `json:"items"`
}

// configListData 纯函数：生效配置 → JSON data 载荷。
func configListData(r *config.Resolved, fileExists bool) configList {
	items := make([]configListItem, 0, len(config.Registry))
	for _, it := range config.Registry {
		e := r.Entries[it.EnvKey]
		v := e.Value
		if it.Secret {
			v = maskSecret(v)
		}
		items = append(items, configListItem{Key: it.TOMLKey(), Env: it.EnvKey, Value: v, Secret: it.Secret, Source: sourceSlug(e.Source)})
	}
	return configList{Path: r.Path, FileExists: fileExists, Items: items}
}

// formatConfigList 纯函数：生效配置 → 表格文本。
func formatConfigList(r *config.Resolved, fileExists bool) string {
	var b strings.Builder
	status := "存在"
	if !fileExists {
		status = "不存在，room config set 时创建"
	}
	fmt.Fprintf(&b, "配置文件: %s（%s）\n\n", r.Path, status)
	w := tabwriter.NewWriter(&b, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tENV\t值\t来源")
	for _, it := range config.Registry {
		e := r.Entries[it.EnvKey]
		v := e.Value
		if it.Secret {
			v = maskSecret(v)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", it.TOMLKey(), it.EnvKey, truncateValue(v), e.Source)
	}
	w.Flush()
	return b.String()
}

// truncateValue 表格内长值截断显示（完整值用 room config get）。
func truncateValue(v string) string {
	const max = 48
	r := []rune(v)
	if len(r) <= max {
		return v
	}
	return string(r[:max]) + "…"
}

func newConfigUnsetCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "unset KEY",
		Short: "从 config.toml 删除一项配置",
		Args:  validationArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			tomlKey, envKey := "", ""
			it, err := lookupItem(args[0])
			switch {
			case err == nil:
				tomlKey, envKey = it.TOMLKey(), it.EnvKey
			case config.RemovedTOMLKey(args[0]) != "":
				// 已移除 schema 的键可能残留在 config.toml 的未识别项中，同样允许清理
				tomlKey = config.RemovedTOMLKey(args[0])
			default:
				return err
			}

			doc, err := config.ReadFile(a.cfg.Path)
			if err != nil {
				return fmt.Errorf("读取 %s 失败: %w", a.cfg.Path, err)
			}
			existed := false
			if envKey != "" {
				_, existed = doc.Values[envKey]
				delete(doc.Values, envKey)
			} else {
				section, key, _ := strings.Cut(tomlKey, ".")
				_, existed = doc.Extra[section][key]
				delete(doc.Extra[section], key)
				if len(doc.Extra[section]) == 0 {
					delete(doc.Extra, section)
				}
			}
			if existed {
				if err := config.WriteFile(a.cfg.Path, doc); err != nil {
					return err
				}
			}
			if a.jsonOut {
				return output.WriteSuccess(cmd.OutOrStdout(), struct {
					Key     string `json:"key"`
					Env     string `json:"env,omitempty"`
					Path    string `json:"path"`
					Removed bool   `json:"removed"` // false 表示本来就未设置（幂等成功）
				}{tomlKey, envKey, a.cfg.Path, existed}, nil)
			}
			if !existed {
				fmt.Fprintf(cmd.OutOrStdout(), "%s 未在 %s 中设置，无需删除\n", tomlKey, a.cfg.Path)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "已从 %s 删除 %s\n", a.cfg.Path, tomlKey)
			return nil
		},
	}
}

func newConfigPathCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "打印全局配置文件路径",
		Args:  validationArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.jsonOut {
				return output.WriteSuccess(cmd.OutOrStdout(), struct {
					Path string `json:"path"`
				}{a.cfg.Path}, nil)
			}
			fmt.Fprintln(cmd.OutOrStdout(), a.cfg.Path)
			return nil
		},
	}
}
