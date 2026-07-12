---
name: room
description: >-
  用 room CLI（飞书会议室预订工具）预订、查询、取消会议室，管理凭证与配置。
  当用户要求订会议室、约会议、查会议室日程、取消会议/日程、初始化飞书应用凭证、
  用户授权登录、配置周期性自动订会（TASK_FORMAT），或提到 room book / list /
  cancel / init / login / auto / notify 任一命令时，务必使用本 skill——即使用户
  没有明确说出「room」这个工具名，只要意图是操作飞书会议室或其预订配置就适用。
metadata:
  requires:
    bins: ["room"]
---

# room CLI — agent 操作指南

room 是基于飞书开放平台的会议室预订 CLI。本 skill 描述其机读契约与非交互用法。
**所有命令一律加 `--json`**：输出变为可解析信封，且隐含非交互（缺必填项立即报错，
绝不挂起等待输入）。

> 本文件是 CLI 行为契约的单一事实源之一：修改 CLI 的信封字段、退出码或
> flag 语义时必须同步更新本文件。

## 输出契约

- **stdout**：每行一个成功信封 `{"ok":true,"data":...,"meta":...}`（NDJSON，
  init/login 阻塞模式会依次输出两行事件）。
- **stderr**：日志（`--json` 下已降噪到 Warn 级）+ 失败时**最后一行**是错误信封
  `{"ok":false,"error":{"type","message","hint","retryable","detail"}}`。
- 判断成败只看**退出码与 `ok` 字段**，不要解析中文文案。`hint` 是给你的下一步
  命令式指引，可直接照做。
- 例外：退出码为 2 且 stderr 最后一行不是 JSON 时，说明 flag 解析在 `--json`
  生效前就失败了（用法错误），按 usage 错误处理。

## 退出码

| 码 | 含义 | 对应 error.type |
|---|---|---|
| 0 | 成功。**book 的 exit 0 ⟺ 房间真的订上了** | — |
| 1 | API/业务失败，含 book 未订到 | `api` `no_room` `conflict` `holiday_skipped` `no_participants` `internal` |
| 2 | 参数校验失败（含非交互缺必填 flag） | `validation` |
| 3 | 认证或配置缺失 | `auth` `config` |
| 10 | 需显式确认，加 `--yes` 或 `--force` 重试 | `confirmation_required` |

`retryable:true` 表示原样重试有意义（如设备码过期）；否则先按 `hint` 修正再试。

## 非交互命令速查

```bash
room config list --json                        # 查看全部配置与来源（secret 掩码）
room config get feishu.app_id --json           # 单项明文值（供脚本）
room config set booking.email_domain corp.com --json
room list -d 7 --json                          # data.events[].event_id 供 cancel 用
room book -d 07-15 -t 14:00-15:00 --title 周会 -p "alice bob" --json
room cancel --event-id <event_id> --yes --json # 幂等：已删除也 exit 0
room auto --dryrun --json                      # 演练（不真订）；不加 --dryrun 即真实批量预订
room notify "文本" --json
```

`ROOM_CONFIG=<path>` 可隔离配置文件（测试/多环境用）。

## 典型工作流

### 首次配置（一次性）

1. `room config list --json` 检查缺什么。
2. 应用凭证：`room init --no-wait --json` → 把 `data.verification_uri_complete`
   给用户在浏览器完成授权 → 用 `data.device_code` 的值运行
   `room init --device-code <device_code> --json` → 凭证写入 config.toml。
   把 device_code 作为单个命令参数传入，**不要把 `data.resume_command` 整串交给
   shell 求值**（它来自网络响应，防篡改注入）。已有凭证会 exit 10，确认后加 `--force`。
3. 用户授权（推荐，以本人身份订，且预订时自动把本人加入参会人）：
   `room login --no-wait --json` → 同样两段式，用 `data.device_code` 运行
   `room login --device-code <device_code> --json`。成功信封带 `user_id`/`name`。
4. 必填项：`room config set booking.room_list <逗号分隔会议室名>`。

两段式适合 agent：第一段立即返回不阻塞，用户授权后第二段恢复。若你能长时间等待，
也可直接跑阻塞模式（`room init --json` / `room login --json`），授权完成前进程不退出。

### 预订会议室

```bash
room book -d 07-15 -t 14:00-15:00 --title 架构评审 -p "alice bob" --json
```

- 成功（exit 0）：`data` 含 `event_id`、实际选中的 `room.name`、`date`、
  `start_time`、`end_time`。把房间名与时间报告给用户。
- 失败（exit 1）按 `error.type` 处置：
  - `no_room`：该时段无可用会议室 → 换时段重试，或建议用户调整
    `booking.room_list` / `booking.room_size` / `booking.room_level_id`。
  - `conflict`：用户日历该时段已有日程 → `room list --json` 查看后换时间。
  - `holiday_skipped`：目标日期是节假日 → 换非节假日日期。
  - `no_participants`：无有效参会人（未 `room login` 且 `-p` 为空或全部解析失败，
    `detail.participants_unresolved` 列出失败项）→ 让用户 `room login`，或补
    `-p` 并检查 `booking.email_domain` 配置。
- 日期只写 `MM-DD` 会自动补当年年份；时间段格式 `HH:MM-HH:MM`。
- 解析出的时间已过时（exit 2，detail 带 `parsed_date`）：自行换未来日期重试，
  不要期望 CLI 替你改期。

### 取消会议

```bash
room list --json                                # 先拿 event_id
room cancel --event-id <event_id> --yes --json  # data.status: cancelled | already_cancelled
```

`already_cancelled` 也是成功（幂等），不要当作失败重试。

## 好/坏示例

```bash
# 好：显式参数 + --json，一步到位
room book -d 07-15 -t 14:00-15:00 --title 周会 --json

# 坏：无参数运行会因非交互缺输入直接 exit 2（不会挂起，但也订不上）
room book --json

# 好：cancel 用 event_id + --yes
room cancel --event-id efab123 --yes --json

# 坏：交互式 cancel 在非终端下 exit 2；缺 --yes 则 exit 10
room cancel --json

# 坏：给 book 加 --dryrun 以为能演练——book 不支持演练（exit 2），总是真实预订
room book -d 07-15 -t 14:00-15:00 --dryrun --json
```

## 注意事项

- **book 总是真实预订**，不支持 `--dryrun`（传入 exit 2）；预订前向用户确认
  时间、标题与参会人。
- **`auto` 默认即真实批量预订**；`--dryrun` 演练（results[].status=planned）。
  拿不准配置是否正确时先 `room auto --dryrun --json` 看计划再真跑。
  批量整体 exit 0，逐条结果看 `data.results[].status`
  （planned/booked/no_room/conflict/holiday_skipped/no_participants/failed）。
- 全局 `--dryrun` 仅 auto 支持，其余命令传入直接 exit 2（绝不静默真实执行）。
- 自然语言输入（`room book "明天下午3点 周会"`）依赖 `nlp.api_key` 配置且解析
  可能失败；agent 优先用显式 `-d/-t/--title`，把自然语言解析留给你自己完成。
- secret 值只有 `room config get` 输出明文，不要把它回显给用户或写入日志。
- 周期性订会配置（TASK_FORMAT DSL）见 [references/task-format.md](references/task-format.md)。
