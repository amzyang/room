package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/amzyang/room/config"
)

// newConfigCmd config 命令族：查看与修改全局配置文件（默认 ~/.config/room/config.toml）。
func newConfigCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "查看与修改配置（全局 config.toml）",
		Long: "管理全局配置文件。KEY 接受 TOML 键（feishu.app_id）或环境变量名（FEISHU_APP_ID）。\n" +
			"优先级：shell 环境变量 > config.toml > 内置默认。\n" +
			"不带子命令且在终端中运行时，进入交互式表单编辑全部配置。",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
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
	)
	return cmd
}

func newConfigSetCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "set KEY VALUE",
		Short: "写入一项配置到 config.toml",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			it, err := config.Lookup(args[0])
			if err != nil {
				return err
			}
			val, err := it.Normalize(args[1])
			if err != nil {
				return err
			}
			doc, err := config.ReadFile(a.cfg.Path)
			if err != nil {
				return fmt.Errorf("读取 %s 失败: %w", a.cfg.Path, err)
			}
			doc.Values[it.EnvKey] = val
			if err := config.WriteFile(a.cfg.Path, doc); err != nil {
				return err
			}
			display := val
			if it.Secret {
				display = maskSecret(val)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "已写入 %s: %s = %s\n", a.cfg.Path, it.TOMLKey(), display)
			warnOverride(cmd, a, it)
			return nil
		},
	}
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			it, err := config.Lookup(args[0])
			if err != nil {
				return err
			}
			e := a.cfg.Entries[it.EnvKey]
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
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, statErr := os.Stat(a.cfg.Path)
			fmt.Fprint(cmd.OutOrStdout(), formatConfigList(a.cfg, statErr == nil))
			return nil
		},
	}
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			it, err := config.Lookup(args[0])
			if err != nil {
				return err
			}
			doc, err := config.ReadFile(a.cfg.Path)
			if err != nil {
				return fmt.Errorf("读取 %s 失败: %w", a.cfg.Path, err)
			}
			if _, ok := doc.Values[it.EnvKey]; !ok {
				fmt.Fprintf(cmd.OutOrStdout(), "%s 未在 %s 中设置，无需删除\n", it.TOMLKey(), a.cfg.Path)
				return nil
			}
			delete(doc.Values, it.EnvKey)
			if err := config.WriteFile(a.cfg.Path, doc); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "已从 %s 删除 %s\n", a.cfg.Path, it.TOMLKey())
			return nil
		},
	}
}

func newConfigPathCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "打印全局配置文件路径",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), a.cfg.Path)
			return nil
		},
	}
}
