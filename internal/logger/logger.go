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
	InitWriter(os.Stderr)
}

// InitWriter is Init writing to w. Color is auto-detected only for a terminal file.
func InitWriter(w io.Writer) {
	var handler slog.Handler
	if config.LogFormat == "json" {
		handler = slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level:       config.LogLevel,
			ReplaceAttr: log.JSONReplaceAttr,
		})
	} else {
		handler = prettyHandler(w, !colorEnabled(isTerminal(w), os.Getenv))
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	slog.SetLogLoggerLevel(slog.LevelInfo)
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isatty.IsTerminal(f.Fd())
}

// colorEnabled follows the NO_COLOR and CLICOLOR_FORCE conventions, so a
// parent that reads a child's stderr through a pipe can still get color.
func colorEnabled(tty bool, getenv func(string) string) bool {
	if getenv("NO_COLOR") != "" {
		return false
	}
	if f := getenv("CLICOLOR_FORCE"); f != "" && f != "0" {
		return true
	}
	return tty
}

// prettyHandler builds the tint+scope-prefix handler for human-readable output.
func prettyHandler(w io.Writer, noColor bool) slog.Handler {
	prefix := slogpfx.ColorizePrefix(slogpfx.DefaultPrefixFormatter)
	if noColor {
		prefix = slogpfx.DefaultPrefixFormatter
	}
	return slogpfx.NewHandler(
		tint.NewHandler(w, &tint.Options{
			Level:       config.LogLevel,
			NoColor:     noColor,
			ReplaceAttr: log.PrettyReplaceAttr,
			TimeFormat:  time.DateTime,
		}),
		&slogpfx.HandlerOptions{
			PrefixKeys:      []string{"scope"},
			PrefixFormatter: prefix,
		},
	)
}

// NewBufferLogger builds a pretty, color, scope-prefixed logger writing to w, with
// formatting that matches `argus start`. Color stays on even when w is not a terminal.
func NewBufferLogger(w io.Writer) *slog.Logger {
	return slog.New(prettyHandler(w, false))
}

func New(ctx context.Context, args ...any) *Logger {
	return log.New(ctx, args...)
}

func Scoped(scope string) *Logger {
	return New(context.Background(), "scope", scope)
}
