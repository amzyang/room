# TASK_FORMAT — auto 命令的周期任务 DSL

`room auto` 按 `booking.task_format`（env `TASK_FORMAT`）批量预订。写入配置：

```bash
room config set booking.task_format '<DSL>' --json
```

## 语法

```
dayOfWeek,startTime-endTime,frequency[:interval[:startDate]],participants,title
```

- `dayOfWeek`：`mon` / `tue` / `wed` / `thu` / `fri` / `sat` / `sun`
- `startTime-endTime`：`HH:MM:SS-HH:MM:SS`
- `frequency`：`weekly` / `daily` / `monthly`；`weekly:2` 表示隔周；
  `weekly:2:2025-04-21` 以 2025-04-21 为周期锚点
- `participants`：`:` 分隔；邮箱前缀自动补 `@EMAIL_DOMAIN`，`oc_` 前缀视为群聊 ID
- 多任务用 `|` 分隔

## 示例

```
fri,11:00:00-12:00:00,weekly,alice:bob,项目周会|mon,17:30:00-18:30:00,weekly:2:2025-04-21,oc_xxxx,双周例会
```

## 运行

```bash
room auto --dryrun --json # 演练：输出计划（status=planned），不真实预订
room auto --json          # 真实批量预订
```

节假日过滤依赖 `booking.tianapi_key`（未配置则节假日不过滤）。
预订窗口上限取会议室预订策略的可提前天数（默认 15 天）。
