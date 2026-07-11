// Package logx 提供与原 Node 版 logtape 输出风格一致的 slog 日志：
// 时间戳 YYYY-MM-DD HH:mm:ss.SSS，全部写入 stderr，保证 stdout 只承载命令输出。
package logx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

type textHandler struct {
	mu    *sync.Mutex
	w     io.Writer
	level slog.Level
	loc   *time.Location
}

func (h *textHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *textHandler) Handle(_ context.Context, r slog.Record) error {
	ts := r.Time.In(h.loc).Format("2006-01-02 15:04:05.000")
	line := fmt.Sprintf("%s %s %s", ts, levelTag(r.Level), r.Message)
	r.Attrs(func(a slog.Attr) bool {
		line += fmt.Sprintf(" %s=%v", a.Key, a.Value)
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := fmt.Fprintln(h.w, line)
	return err
}

func (h *textHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *textHandler) WithGroup(_ string) slog.Handler      { return h }

func levelTag(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERR"
	case l >= slog.LevelWarn:
		return "WRN"
	case l >= slog.LevelInfo:
		return "INF"
	default:
		return "DBG"
	}
}

// New 构建写入 stderr 的 logger；debug 为 true 时输出 debug 级别日志。
func New(debug bool, loc *time.Location) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	return slog.New(&textHandler{mu: &sync.Mutex{}, w: os.Stderr, level: level, loc: loc})
}
