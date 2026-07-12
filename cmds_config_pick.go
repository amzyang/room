package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/amzyang/room/config"
	"github.com/amzyang/room/feishu"
	"github.com/amzyang/room/output"
)

// levelLister 层级选择器对飞书 API 的最小依赖。
type levelLister interface {
	GetRoomLevelChildren(ctx context.Context, parentID string) ([]feishu.RoomLevel, error)
}

// selectLevelFunc 展示一层菜单并返回用户选择（huh 适配或测试脚本）。
type selectLevelFunc func(title string, opts []levelOption) (levelOption, error)

type levelAction int

const (
	levelActionPick  levelAction = iota // 选定 Level 写入配置
	levelActionEnter                    // 下钻进 Level 的子层级
	levelActionUp                       // 返回上一级
)

type levelOption struct {
	Label  string
	Action levelAction
	Level  feishu.RoomLevel
}

// buildLevelMenu 纯函数：当前路径 + 子层级 → 菜单项。
// 非根层级前置「就选这一级 / 返回上一级」；有子级的节点下钻，叶子直选。
func buildLevelMenu(path, children []feishu.RoomLevel) []levelOption {
	var opts []levelOption
	if len(path) > 0 {
		cur := path[len(path)-1]
		opts = append(opts,
			levelOption{Label: fmt.Sprintf("✔ 就选这一级（%s）", cur.Name), Action: levelActionPick, Level: cur},
			levelOption{Label: ".. 返回上一级", Action: levelActionUp},
		)
	}
	for _, c := range children {
		o := levelOption{Label: c.Name, Action: levelActionPick, Level: c}
		if c.HasChild {
			o.Label += " ▸"
			o.Action = levelActionEnter
		}
		opts = append(opts, o)
	}
	return opts
}

func levelPathTitle(path []feishu.RoomLevel) string {
	if len(path) == 0 {
		return "当前位置: 根层级"
	}
	names := make([]string, len(path))
	for i, l := range path {
		names[i] = l.Name
	}
	return "当前位置: " + strings.Join(names, " / ")
}

// pickRoomLevel 逐级下钻直到用户选定一个层级。
func pickRoomLevel(ctx context.Context, api levelLister, selectLevel selectLevelFunc) (feishu.RoomLevel, error) {
	var path []feishu.RoomLevel
	for {
		parentID := ""
		if len(path) > 0 {
			parentID = path[len(path)-1].RoomLevelID
		}
		children, err := api.GetRoomLevelChildren(ctx, parentID)
		if err != nil {
			return feishu.RoomLevel{}, fmt.Errorf("查询会议室层级失败: %w", err)
		}
		if len(path) == 0 && len(children) == 0 {
			return feishu.RoomLevel{}, fmt.Errorf("未查询到任何会议室层级（检查应用是否有 vc:room:readonly 权限）")
		}
		choice, err := selectLevel(levelPathTitle(path), buildLevelMenu(path, children))
		if err != nil {
			return feishu.RoomLevel{}, err
		}
		switch choice.Action {
		case levelActionUp:
			path = path[:len(path)-1]
		case levelActionEnter:
			path = append(path, choice.Level)
		case levelActionPick:
			return choice.Level, nil
		}
	}
}

// selectLevelHuh pickRoomLevel 的 huh 适配：一层菜单一个 Select。
func selectLevelHuh(title string, opts []levelOption) (levelOption, error) {
	idx := 0
	options := make([]huh.Option[int], len(opts))
	for i, o := range opts {
		options[i] = huh.NewOption(o.Label, i)
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[int]().Title("booking.room_level_id").Description(title).
			Options(options...).Value(&idx),
	)).WithAccessible(os.Getenv("ACCESSIBLE") != "")
	if err := form.Run(); err != nil {
		return levelOption{}, err
	}
	return opts[idx], nil
}

// runConfigPickRoomLevel 拉取飞书层级树交互选择 booking.room_level_id 并写入 config.toml。
func runConfigPickRoomLevel(cmd *cobra.Command, a *app) error {
	appID := env("FEISHU_APP_ID")
	appSecret := env("FEISHU_APP_SECRET")
	if appID == "" || appSecret == "" {
		return output.Errf(output.TypeConfig,
			"运行 room init，或 room config set feishu.app_id / feishu.app_secret 写入凭证",
			"缺失飞书应用凭证（FEISHU_APP_ID / FEISHU_APP_SECRET）")
	}
	if !a.interactive() {
		return output.Errf(output.TypeValidation,
			"非交互环境请用 room config set booking.room_level_id VALUE 直接写入",
			"交互选择需要在终端中运行")
	}

	api := feishu.NewAPI(feishu.Config{
		AppID:         appID,
		AppSecret:     appSecret,
		AuthMode:      feishu.AuthMode(env("FEISHU_AUTH_MODE")),
		UserTokenPath: userTokenPath(),
		Debug:         a.debug,
	}, feishu.NewHTTPClient(), a.log, a.now, a.loc)

	level, err := pickRoomLevel(cmd.Context(), api, selectLevelHuh)
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(cmd.OutOrStdout(), "已取消，未写入")
			return nil
		}
		return err
	}

	it, _ := config.ByEnvKey("ROOM_LEVEL_ID")
	val, err := setConfigValue(a, it, level.RoomLevelID)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "已写入 %s: %s = %s（%s）\n", a.cfg.Path, it.TOMLKey(), val, level.Name)
	warnOverride(cmd, a, it)
	return nil
}
