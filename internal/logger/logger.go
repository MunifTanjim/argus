// Package logger is argus's logging entry point: it reads the resolved config
// (config.LogLevel / config.LogFormat) and installs the global slog handler via Init,
// and exposes scoped loggers. The portable Logger type, custom levels, and attribute
// rendering live in the sibling logger/log package, which knows nothing about config —
// keeping config-aware wiring separate from the reusable logging core.
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

// Init builds the global slog handler from the resolved config (config.LogLevel and
// config.LogFormat) and installs it as the default. It must be called once, after the
// config is loaded, before operational logging begins. config.LogLevel is a
// *slog.LevelVar, so later level changes still take effect on the built handler.
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

// prettyHandler builds the tint+scope-prefix handler used for human-readable
// output, at the globally configured level (config.LogLevel). It is the single
// source of the stderr format so anything reusing it (e.g. NewBufferLogger)
// stays in lockstep with `argus start`.
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

// NewBufferLogger builds a logger that writes pretty, color, scope-prefixed
// records to w at the globally configured level (config.LogLevel). The TUI uses
// it to capture the embedded node's logs into an in-memory buffer and tail them
// in its Logs tab, so the formatting matches `argus start` exactly. Color stays
// on (NoColor false): the buffer is rendered in the TUI's interactive alt-screen,
// not a pipe, and we want parity with the stderr logs (faded keys). The tab
// renders the buffered lines as-is, so the escapes pass through to the terminal;
// the view windows by line count and never truncates mid-line, so ANSI is safe.
func NewBufferLogger(w io.Writer) *slog.Logger {
	return slog.New(prettyHandler(w, false))
}

func New(ctx context.Context, args ...any) *Logger {
	return log.New(ctx, args...)
}

func Scoped(scope string) *Logger {
	return New(context.Background(), "scope", scope)
}
