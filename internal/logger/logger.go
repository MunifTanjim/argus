// Package logger is argus's logging entry point: it installs the global slog handler
// from config and exposes scoped loggers. The reusable logging core (Logger type,
// custom levels, attr rendering) lives in the sibling logger/log package, kept free
// of config dependencies.
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/dpotapov/slogpfx"
	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"

	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/logger/log"
)

type Level = slog.Level

const (
	LevelTrace = log.LevelTrace
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
	LevelFatal = log.LevelFatal
)

type Logger = log.Logger

// Init builds the global slog handler from config and installs it as default. Call
// once after config is loaded. config.LogLevel is a *slog.LevelVar, so later level
// changes still take effect on the built handler.
func Init() {
	w := os.Stderr

	var handler slog.Handler
	if config.LogFormat == "json" {
		handler = slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level:       config.LogLevel,
			ReplaceAttr: log.JSONReplaceAttr,
		})
	} else {
		handler = prettyHandler(w, !isatty.IsTerminal(w.Fd()))
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	slog.SetLogLoggerLevel(slog.LevelInfo)
}

// prettyHandler builds the tint+scope-prefix handler for human-readable output. The
// single source of the stderr format, so reusers (e.g. NewBufferLogger) stay in
// lockstep with `argus start`.
func prettyHandler(w io.Writer, noColor bool) slog.Handler {
	return slogpfx.NewHandler(
		tint.NewHandler(w, &tint.Options{
			Level:       config.LogLevel,
			NoColor:     noColor,
			ReplaceAttr: log.PrettyReplaceAttr,
			TimeFormat:  time.DateTime,
		}),
		&slogpfx.HandlerOptions{
			PrefixKeys: []string{"scope"},
		},
	)
}

// NewBufferLogger builds a pretty, color, scope-prefixed logger writing to w, used
// by the TUI to tail the embedded node's logs in its Logs tab with formatting that
// matches `argus start`. Color stays on for parity with stderr logs: the tab renders
// in the alt-screen (not a pipe) and never truncates mid-line, so ANSI is safe.
func NewBufferLogger(w io.Writer) *slog.Logger {
	return slog.New(prettyHandler(w, false))
}

func New(ctx context.Context, args ...any) *Logger {
	return log.New(ctx, args...)
}

func Scoped(scope string) *Logger {
	return New(context.Background(), "scope", scope)
}
