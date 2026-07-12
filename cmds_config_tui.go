package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/amzyang/room/config"
	"github.com/amzyang/room/feishu"
)

// runConfigTUI 分组表单编辑全部配置项，走完保存写回全局 config.toml。
// 表单结构与校验在 config.BuildFormSpec（纯函数，有测试）；此处只做 huh 的机械翻译。
func runConfigTUI(cmd *cobra.Command, a *app) error {
	doc, err := config.ReadFile(a.cfg.Path)
	if err != nil {
		return fmt.Errorf("读取 %s 失败（可手动修复或删除后重试）: %w", a.cfg.Path, err)
	}

	effective := make(map[string]string, len(a.cfg.Entries))
	for k, e := range a.cfg.Entries {
		effective[k] = e.Value
	}
	groups := config.BuildFormSpec(effective, a.loadLevelOptions(cmd))

	strVals := map[string]*string{}
	var huhGroups []*huh.Group
	for _, g := range groups {
		var fields []huh.Field
		for _, f := range g.Fields {
			fields = append(fields, buildHuhField(f, strVals))
		}
		huhGroups = append(huhGroups, huh.NewGroup(fields...).Title(g.Title))
	}

	form := huh.NewForm(huhGroups...).WithAccessible(os.Getenv("ACCESSIBLE") != "")
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(cmd.OutOrStdout(), "已取消，未保存")
			return nil
		}
		return err
	}

	results := make(map[string]string, len(strVals))
	for k, p := range strVals {
		results[k] = *p
	}
	if err := config.ApplyFormResult(doc, results); err != nil {
		return err
	}
	if err := config.WriteFile(a.cfg.Path, doc); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "已保存 %s\n", a.cfg.Path)

	// 被更高层（shell env）压住的项，保存了也不生效，当场列出免得用户困惑
	var overridden []string
	for _, it := range config.Registry {
		if src := a.cfg.OverrideOf(it.EnvKey); src != config.SourceUnset {
			overridden = append(overridden, fmt.Sprintf("%s（%s）", it.TOMLKey(), src))
		}
	}
	if len(overridden) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "注意: 以下项被更高优先级来源覆盖，config.toml 中的值在当前环境不生效: %s\n",
			strings.Join(overridden, "、"))
	}
	return nil
}

// loadLevelOptions 表单打开前拉取会议室层级树拍平为下拉选项。降级契约:
// 凭证缺失(首次配置的正常路径)与根为空静默返回 nil;拉取失败/超时 stderr 一行提示
// 后返回 nil——任何情况都不挡住表单,nil 时 room_level_id 回退文本输入。
func (a *app) loadLevelOptions(cmd *cobra.Command) []config.Option {
	if env("FEISHU_APP_ID") == "" || env("FEISHU_APP_SECRET") == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
	defer cancel()
	opts, err := levelSelectOptions(ctx, a.newFeishuAPI(feishu.NewHTTPClient()))
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "拉取会议室层级失败，room_level_id 使用文本输入: %v\n", err)
		return nil
	}
	return opts
}

func buildHuhField(f config.FieldSpec, strVals map[string]*string) huh.Field {
	switch f.Kind {
	case config.FieldSelect:
		v := f.Initial
		strVals[f.Item.EnvKey] = &v
		opts := make([]huh.Option[string], len(f.Options))
		for i, o := range f.Options {
			opts[i] = huh.NewOption(o.Label, o.Value)
		}
		return huh.NewSelect[string]().Title(f.Title).Description(f.Desc).
			Options(opts...).Value(&v)
	case config.FieldText:
		v := f.Initial
		strVals[f.Item.EnvKey] = &v
		return huh.NewText().Title(f.Title).Description(f.Desc).Lines(3).
			Value(&v).Validate(f.Validate)
	default:
		v := f.Initial
		strVals[f.Item.EnvKey] = &v
		in := huh.NewInput().Title(f.Title).Description(f.Desc).Value(&v).Validate(f.Validate)
		if f.Masked {
			in = in.EchoMode(huh.EchoModePassword)
		}
		return in
	}
}
