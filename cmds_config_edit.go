package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/amzyang/room/booking"
	"github.com/amzyang/room/config"
	"github.com/amzyang/room/output"
)

// resolveEditor 依次取 VISUAL、EDITOR（POSIX 惯例），都缺报 validation。
func resolveEditor(visual, editor string) (string, error) {
	if v := strings.TrimSpace(visual); v != "" {
		return v, nil
	}
	if e := strings.TrimSpace(editor); e != "" {
		return e, nil
	}
	return "", output.Errf(output.TypeValidation,
		"设置 EDITOR 环境变量（如 export EDITOR=vim），或改用 room config set", "未设置 $VISUAL / $EDITOR")
}

func newConfigEditCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "用 $VISUAL/$EDITOR 直接编辑 config.toml（保存后校验并提示问题）",
		Long: "在编辑器中直接修改全局配置文件；文件不存在时先生成带注释的骨架。\n" +
			"保存退出后重新解析并逐项校验，问题以警告输出（不回滚，文件以你保存的为准）。",
		Args: validationArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !a.interactive() {
				return output.Errf(output.TypeValidation,
					"编辑器需要终端；脚本请用 room config set/unset（支持 --json）",
					"config edit 仅支持交互终端")
			}
			editor, err := resolveEditor(os.Getenv("VISUAL"), os.Getenv("EDITOR"))
			if err != nil {
				return err
			}
			path := a.cfg.Path
			// 文件不存在先落一份带注释的骨架，编辑器里有结构可循
			if _, err := os.Stat(path); os.IsNotExist(err) {
				if err := config.WriteFile(path, config.NewDocument()); err != nil {
					return err
				}
			}
			if err := a.editFile(editor, path); err != nil {
				return fmt.Errorf("编辑器退出异常: %w", err)
			}
			warnings := lintConfigFile(path)
			for _, warn := range warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "警告: "+warn)
			}
			if len(warnings) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "已保存 %s\n", path)
			}
			return nil
		},
	}
}

// editFile 编辑器执行缝隙：测试注入 a.runEditor，生产 exec 并接管终端。
func (a *app) editFile(editor, path string) error {
	if a.runEditor != nil {
		return a.runEditor(editor, path)
	}
	parts := strings.Fields(editor) // 支持 "code --wait" 形式
	c := exec.Command(parts[0], append(parts[1:], path)...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

// lintConfigFile 保存后校验：解析失败或字段非法只告警不回滚（文件以用户保存的为准，
// 与 Bootstrap 对坏配置的容忍策略一致）。
func lintConfigFile(path string) []string {
	doc, err := config.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("%s 无法解析: %v（room 将忽略该文件直至修复）", path, err)}
	}
	var warns []string
	for _, it := range config.Registry {
		v, ok := doc.Values[it.EnvKey]
		if !ok {
			continue
		}
		if err := it.Validate(v); err != nil {
			warns = append(warns, err.Error())
			continue
		}
		if it.EnvKey == "TASK_FORMAT" {
			if err := booking.ValidateTaskFormat(v); err != nil {
				warns = append(warns, fmt.Sprintf("%s: %v", it.TOMLKey(), err))
			}
		}
	}
	return warns
}
