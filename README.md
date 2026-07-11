# room

会议室预订自动化 CLI，基于飞书（Lark）开放平台 API。Go 语言重写自内部 Node.js 项目
room-cheating，单二进制分发，支持 Docker 构建与 Homebrew 安装。

> 本项目仅供学习与研究使用。

## 安装

```bash
# Homebrew
brew install amzyang/tap/room

# 或从源码构建
go build -o room .

# 或 Docker
./build.sh
```

## 命令

```bash
room auto            # 按 TASK_FORMAT 自动预订（默认演练模式，--run 实际执行）
room book [input]    # 智能预订：自然语言（需 OPENAI_API_KEY）或 -d/-t/-p 参数
room list [-d 31]    # 列出未来 N 天的日历事件
room cancel [-d 31]  # 交互式取消自己组织的事件
room login           # OAuth 设备码流程授权用户身份
room notify [text]   # 通过自定义机器人 webhook 发送文本消息
```

全局标志：`--run` 实际执行（默认演练）、`--debug` 输出 HTTP 请求详情。

示例：

```bash
room book "tom 2pm 1h shikai 团队周会"
room book -d 12-01 -t 10:00-11:00 -p "alice bob oc_xxxx"
room list -d 7
```

## 配置

复制 `.env.example` 为 `.env` 并填写。必填项：

| 变量 | 说明 |
|---|---|
| `FEISHU_APP_ID` / `FEISHU_APP_SECRET` | 飞书自建应用凭据 |
| `TASK_OWNER` | 负责人邮箱前缀，自动加入所有会议 |
| `ROOM_LIST` | 会议室优先级列表（逗号分隔） |

常用可选项：`ROOM_LEVEL_ID`（按楼层层级查找）、`ROOM_EXCLUDE_LIST`、`ROOM_SIZE`、
`TASK_FORMAT`（auto 命令的任务 DSL）、`EMAIL_DOMAIN`（参与者邮箱域）、
`TIANAPI_KEY`（节假日过滤）、`SENTRY_DSN`（错误上报）、`OPENAI_API_KEY`（NLP）。
完整说明见 [.env.example](.env.example)。

### TASK_FORMAT

```
dayOfWeek,startTime-endTime,frequency[:interval[:startDate]],participants,title
```

- `frequency`：`weekly` / `daily` / `monthly`；`weekly:2` 表示隔周；
  `weekly:2:2025-04-21` 以 2025-04-21 为周期锚点
- `participants`：`:` 分隔；邮箱前缀自动补 `@EMAIL_DOMAIN`，`oc_` 前缀视为群聊 ID
- 多任务用 `|` 分隔

```
TASK_FORMAT="fri,11:00:00-12:00:00,weekly,alice:bob,项目周会|mon,17:30:00-18:30:00,weekly:2:2025-04-21,oc_xxxx,双周例会"
```

## 飞书应用权限

在飞书开发者后台「权限管理 → 批量导入」中导入 [permissions.json](permissions.json)。
用户身份预订需先运行 `room login` 完成 OAuth 设备码授权（凭证存于
`.cache/feishu-user-token.json`，授权硬顶一年，到期需重新 login）。

## 定时任务部署

```bash
# 构建镜像
./build.sh

# crontab：工作日每天 9 点自动订会（输出追加到 booking.log）
0 9 * * 1-5 cd /path/to/room && ./run.sh >> booking.log 2>&1
```

`run.sh` 通过 Docker 运行并挂载 `.cache/`（持久化用户凭证与节假日缓存），
宿主机 `.cache/` 目录需对容器内 uid 1001 可写。

## 开发

```bash
go test ./...    # 单元测试
go vet ./...
go build -o room .
```

发布：打 tag（`v*`）推送后由 GitHub Actions goreleaser 构建
darwin/linux × amd64/arm64 并更新 Homebrew tap（需配置仓库 secret
`HOMEBREW_TAP_GITHUB_TOKEN`）。

## 安全说明

默认 `ROOM_TLS_INSECURE=1` 跳过 TLS 证书校验，这是从原项目继承的行为
（兼容内网 TLS 中间人代理环境）。在证书链正常的环境建议设 `ROOM_TLS_INSECURE=0`
恢复严格校验。
