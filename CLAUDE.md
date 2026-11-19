# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

`room` 是一个单二进制 Go CLI，自动化飞书会议室预订。它同时服务两类消费者：终端里的人（TUI / 交互式提问）和 agent / 脚本（`--json` 机器契约）。两种契约都必须保持。

## 常用命令

```bash
just check        # fmt + vet + test，提交前跑这个
just test         # go test ./...
just build        # go build -ldflags "-X main.version=$VERSION" -o room .
just e2e          # 只读验证凭证链与飞书 API 连通（真实网络，不创建/取消日程）
go test . -run TestName          # 单个测试
go test ./booking -run TestName
```

`go vet ./...` 是唯一的静态检查；项目没有 golangci-lint 配置，格式只有 gofmt。

变更直接提交到 `main`，Conventional Commits + 中文 subject。

## 分层

根包 `main` 只做 cobra 接线；其余是不知道 cobra 存在的库。无 `internal/`。

- `cmds_<topic>.go` — 命令实现。新增命令 = 新文件 + `newRootCmd` 里的 `AddCommand` + 同主题 `_test.go`。
- `booking/` — 领域编排。`Service` 持有 `API`、`Config`、`Log`、`Clock`、`Loc`、`HTTP`。
- `feishu/` — Lark SDK 之上的传输层。
- `output/` — 机器契约：envelope、错误类型闭集、退出码。
- `config/` — `schema.go` 的 `Registry` 是所有配置键的唯一事实源。
- `logx/` — 自定义 slog handler，只写 stderr。

## 必须守住的不变量

- **不要直接写 stdout。** 命令输出一律走 `cmd.OutOrStdout()`；`--json` 的 stdout 必须是可解析的 NDJSON（成功 envelope 每行一个对象；`init`/`login` 阻塞模式两行）。错误 envelope 必须是 stderr 的最后一行。日志走 `logx` 到 stderr。
- **环境变量只在 `config.Bootstrap` 之后读。** Bootstrap 把 TOML 层的值注入 `os.Setenv`；在包级 var 或 `init()` 里读会拿到注入前的值。新配置键加到 `config/schema.go` 的 `Registry`，不要直接 `os.Getenv` 新名字。
- **错误在入口层定型。** 新面向用户的错误用 `output.Errf(output.TypeXxx, "<可直接照抄的下一条命令>", "<message>")`，包内部仍用 `fmt.Errorf("%w")` 保留 cause。`error.type` 与 `booking.BookResult.Status` 是闭集，新增字符串必须同时加进 `output/errors.go` / `booking/result.go`。
- **`exit 0 ⟺ 命令的后置条件成立`**（`book` 返回 0 表示房间真的订到了）。不要为了「跑了没崩」返回 0。
- **时间永远注入**：`Clock func() time.Time` + `Loc`（`Asia/Shanghai` 只在 `PersistentPreRunE` 设一次）。测试冻结时钟。
- **`skills/room/SKILL.md` 是 CLI 契约的事实源之一**：改 envelope 字段、退出码、flag 语义要在同一次提交里同步更新它。

## 测试

- 无 testify、无 golden 文件、无 `t.Parallel()`——根包的测试通过 `config.Bootstrap` 改写进程环境，并行会串味。
- 复用现成脚手架：`newAgentTestApp(t, svc, stdin)`（非 TTY、固定时钟、可注 fake service）、`execAppCmd`（跑单个子命令）、`execRootCmd`（整棵树，自动关掉 Sentry）。
- 注入点是 `app` 结构体的函数字段（`newService`、`sentryTransport`、`tokenStore`、`runEditor` 等），不要为测试在生产代码里加分支。`booking.API` 接口定义在消费方 `booking` 包里。
- HTTP 用 `httptest.NewServer` fake，不 mock transport。

## 坑

- `plans/` 是 gitignore 掉的本地草稿目录，不是仓库内容；`.env.bak` 含真实凭证，不要读也不提交。
- `.gitignore` 里的 `!output/` 取反是必要的，去掉会让 `output/` 包被全局规则隐藏。
- `--dryrun` 只对 `auto` 有效，其他命令直接报 `type=validation`；这是有意的，别放宽。
- 根命令用 `Args: cobra.ArbitraryArgs` + 自定义 `RunE` 接管未知命令，才能让 `--json` 也产出错误 envelope。改回 `cobra.NoArgs` 会破坏机器契约。
- flag 解析在遇到坏 flag 时提前中止，`--json` 可能没被解析到；`main` 用 `jsonRequested()` 重扫 `os.Args` 兜底。新增早退路径要保持这个行为。
- 交互模式要求 stdin **和** stdout 都是 TTY 且非 `--json`；只有 stdin 是 TTY 按非交互处理，避免管道挂住。
- `config.toml` 损坏是非致命的：`Bootstrap` 记 `Warning` 后继续，好让 `room config` 能修它。
- `room init --force` 会吊销旧 app 的登录并删除缓存里的 token，改动 `finishInit` / `revokeOldLoginBestEffort` 附近时注意凭证丢失的影响面。
- 日历读写只支持用户身份，没有应用身份兜底；未登录时退 3。
- `room keepalive` 存在是因为飞书 refresh_token 约 7 天不用就失效。
- README 提到的 `./build.sh` 不存在，别信。

## 代码风格

- 注释和用户可见文案用中文，标识符 / flag / JSON key 用英文。
- 大量 `s.Log.Info(fmt.Sprintf(...))` 是本仓库的既有写法，不要无理由改成 key/value attrs。
- `context.Context` 一律是第一个参数。
