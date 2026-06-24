// Package logger is argus's logging entry point: it reads the resolved config
// (config.LogLevel / config.LogFormat) and installs the global slog handler via Init,
// and exposes scoped loggers. The portable Logger type, custom levels, and attribute
// rendering live in the sibling logger/log package, which knows nothing about config —
// keeping config-aware wiring separate from the reusable logging core.
package logger

import (
	"context"
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
		handler = slogpfx.NewHandler(
			tint.NewHandler(w, &tint.Options{
				Level:       config.LogLevel,
				NoColor:     !isatty.IsTerminal(w.Fd()),
				ReplaceAttr: log.PrettyReplaceAttr,
				TimeFormat:  time.DateTime,
			}),
			&slogpfx.HandlerOptions{
				PrefixKeys: []string{"scope"},
			},
		)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	slog.SetLogLoggerLevel(slog.LevelInfo)
}

func New(ctx context.Context, args ...any) *Logger {
	return log.New(ctx, args...)
}

func Scoped(scope string) *Logger {
	return New(context.Background(), "scope", scope)
}
