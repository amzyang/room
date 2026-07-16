package main

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	sentry "github.com/getsentry/sentry-go"
	"github.com/spf13/cobra"

	"github.com/amzyang/room/config"
	"github.com/amzyang/room/output"
)

// sentrySelftestFlushTimeout 自检的 flush 等待上限。main 退出时的 2s 是止损值；
// 自检是显式等待场景，取宽松值提高「送达/未送达」判定可靠性。
const sentrySelftestFlushTimeout = 10 * time.Second

// sentryDSNSource DSN 生效来源（JSON 输出用 slug，人类文案见 label）。
type sentryDSNSource string

const (
	sentrySourceFlag     sentryDSNSource = "flag"
	sentrySourceShellEnv sentryDSNSource = "shell_env"
	sentrySourceTOML     sentryDSNSource = "config.toml"
	sentrySourceBuiltin  sentryDSNSource = "builtin"
	sentrySourceUnset    sentryDSNSource = "unset"
	sentrySourceDisabled sentryDSNSource = "disabled"
)

func (s sentryDSNSource) label() string {
	switch s {
	case sentrySourceFlag:
		return "--sentry-dsn flag"
	case sentrySourceShellEnv:
		return "shell 环境变量 SENTRY_DSN"
	case sentrySourceTOML:
		return "config.toml"
	case sentrySourceBuiltin:
		return "编译内置"
	case sentrySourceDisabled:
		return "已显式禁用（--sentry-dsn/SENTRY_DSN 设为空）"
	}
	return "未配置"
}

// resolveSentryDSNStatus 委托 resolveSentryDSN 求生效 DSN（单一事实来源），叠加来源推导。
// 依赖 Bootstrap 不变式：SENTRY_DSN 无默认值（config/schema.go），entry.Source 为
// ShellEnv/TOML ⟺ 进程 env 已设置（resolve 只把这两层注入 env）。
func resolveSentryDSNStatus(flagValue string, flagSet bool, entry config.Entry, builtin string) (string, sentryDSNSource) {
	envSet := entry.Source == config.SourceShellEnv || entry.Source == config.SourceTOML
	dsn := resolveSentryDSN(flagValue, flagSet, entry.Value, envSet, builtin)
	switch {
	case dsn == "" && (flagSet || envSet):
		return "", sentrySourceDisabled
	case dsn == "":
		return "", sentrySourceUnset
	case flagSet:
		return dsn, sentrySourceFlag
	case entry.Source == config.SourceShellEnv:
		return dsn, sentrySourceShellEnv
	case entry.Source == config.SourceTOML:
		return dsn, sentrySourceTOML
	}
	return dsn, sentrySourceBuiltin
}

// sentryStatus 自检状态载荷：--json data 与人类表格共用。
type sentryStatus struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source"`
	Host       string `json:"host,omitempty"`
	ProjectID  string `json:"project_id,omitempty"`
	PublicKey  string `json:"public_key,omitempty"` // 已脱敏，完整值见 DSN 本身
	Release    string `json:"release"`
	DSNError   string `json:"dsn_error,omitempty"`
}

// sentrySendResult 测试事件发送结果。
type sentrySendResult struct {
	ExceptionEventID string `json:"exception_event_id"`
	MessageEventID   string `json:"message_event_id"`
	Flushed          bool   `json:"flushed"`
}

// sentrySelftest --json 的完整 data 载荷。
type sentrySelftest struct {
	sentryStatus
	ClientInit bool              `json:"client_initialized"`
	Sent       *sentrySendResult `json:"sent,omitempty"` // 未配置/禁用时缺省
}

// buildSentryStatus 纯函数：DSN + 来源 → 展示状态。剖析 host/project/public key，
// public key 走 maskSecret 脱敏；解析失败记入 DSNError。
func buildSentryStatus(dsn string, source sentryDSNSource, release string) sentryStatus {
	st := sentryStatus{Configured: dsn != "", Source: string(source), Release: release}
	if dsn == "" {
		return st
	}
	parsed, err := sentry.NewDsn(dsn)
	if err != nil {
		st.DSNError = err.Error()
		return st
	}
	st.Host = parsed.GetHost()
	st.ProjectID = parsed.GetProjectID()
	st.PublicKey = maskSecret(parsed.GetPublicKey())
	return st
}

// formatSentryStatus 纯函数：状态 → 表格文本（风格对齐 formatConfigList）。
func formatSentryStatus(st sentryStatus, clientInit bool) string {
	var b strings.Builder
	b.WriteString("Sentry 自检\n\n")
	w := tabwriter.NewWriter(&b, 2, 4, 2, ' ', 0)
	src := sentryDSNSource(st.Source)
	fmt.Fprintf(w, "来源:\t%s\n", src.label())
	if st.Configured && st.DSNError == "" {
		fmt.Fprintf(w, "Host:\t%s\n", st.Host)
		fmt.Fprintf(w, "Project:\t%s\n", st.ProjectID)
		fmt.Fprintf(w, "Public Key:\t%s\n", st.PublicKey)
	}
	fmt.Fprintf(w, "Release:\t%s\n", st.Release)
	if st.Configured {
		status := "未初始化"
		if clientInit {
			status = "已初始化"
		}
		fmt.Fprintf(w, "Client:\t%s\n", status)
	}
	w.Flush()
	if src == sentrySourceUnset {
		b.WriteString("\n提示: room config set sentry.dsn <DSN> 或设置 SENTRY_DSN 环境变量\n")
	}
	return b.String()
}

// sentrySelftestError 自检专用错误类型：在 Sentry 按类型独立分组，与真实 issue 隔离，
// UI 可辨识、可单独 ignore。
type sentrySelftestError struct{ release string }

func (e *sentrySelftestError) Error() string {
	return fmt.Sprintf("[room-selftest] 测试异常 release=%s", e.release)
}

// eventIDString SDK 契约：nil 表示事件被丢弃。
func eventIDString(id *sentry.EventID) string {
	if id == nil {
		return ""
	}
	return string(*id)
}

func newSentryCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:    "sentry",
		Short:  "Sentry 集成自检：展示 DSN 来源与解析结果，发送测试事件验证上报链路",
		Hidden: true,
		Args:   validationArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			// --sentry-dsn 是 root 的 persistent flag；单测单独执行子命令时不存在，
			// GetString 报错忽略、Changed 恒 false，与 initSentry 同款容错。
			flagValue, _ := cmd.Flags().GetString("sentry-dsn")
			dsn, source := resolveSentryDSNStatus(flagValue, cmd.Flags().Changed("sentry-dsn"),
				a.cfg.Entries["SENTRY_DSN"], builtinSentryDSN)
			st := buildSentryStatus(dsn, source, version)
			out := cmd.OutOrStdout()

			if dsn == "" { // unset / disabled：只报状态，exit 0
				if a.jsonOut {
					return output.WriteSuccess(out, sentrySelftest{sentryStatus: st}, nil)
				}
				fmt.Fprint(out, formatSentryStatus(st, false))
				return nil
			}
			if st.DSNError != "" {
				return output.Errf(output.TypeConfig, "检查 DSN 格式：https://<key>@<host>/<project>",
					"Sentry DSN 无法解析: %s", st.DSNError)
			}
			// 重新 Init：拿回 PersistentPreRun 中被 initSentry 丢弃的错误。
			// Transport 是测试缝隙，nil 时 SDK 用默认 HTTPTransport，与生产同构。
			if err := sentry.Init(sentry.ClientOptions{Dsn: dsn, Release: version, Transport: a.sentryTransport}); err != nil {
				return output.Errf(output.TypeConfig, "检查 DSN 是否有效", "Sentry 初始化失败: %v", err)
			}
			if !a.jsonOut {
				fmt.Fprint(out, formatSentryStatus(st, true))
				fmt.Fprintln(out, "\n发送测试事件（tag e2e=true）...")
			}
			var excID, msgID *sentry.EventID
			sentry.WithScope(func(scope *sentry.Scope) {
				scope.SetTag("e2e", "true")
				scope.SetTag("command", "room-sentry")
				excID = sentry.CaptureException(&sentrySelftestError{release: version})
				msgID = sentry.CaptureMessage(fmt.Sprintf("[room-selftest] 测试消息 release=%s", version))
			})
			if !sentry.Flush(sentrySelftestFlushTimeout) {
				return output.Errf(output.TypeAPI, "检查网络能否访问 "+st.Host,
					"Sentry flush 超时（%v），测试事件可能未送达", sentrySelftestFlushTimeout)
			}
			sent := &sentrySendResult{
				ExceptionEventID: eventIDString(excID),
				MessageEventID:   eventIDString(msgID),
				Flushed:          true,
			}
			if a.jsonOut {
				return output.WriteSuccess(out, sentrySelftest{sentryStatus: st, ClientInit: true, Sent: sent}, nil)
			}
			w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
			fmt.Fprintf(w, "Exception Event ID:\t%s\n", sent.ExceptionEventID)
			fmt.Fprintf(w, "Message Event ID:\t%s\n", sent.MessageEventID)
			fmt.Fprintln(w, "Flush:\t成功")
			w.Flush()
			return nil
		},
	}
}
