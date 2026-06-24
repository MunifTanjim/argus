package logger

import (
	"context"
	"log/slog"
	"testing"

	"github.com/MunifTanjim/argus/internal/config"
)

// TestInitHonorsLevel verifies Init builds a handler bound to config.LogLevel, so the
// resolved level governs what the default logger emits.
func TestInitHonorsLevel(t *testing.T) {
	config.LogLevel.Set(slog.LevelWarn)
	config.LogFormat = "pretty"
	Init()
	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		t.Error("debug should be disabled at warn level")
	}
	if !slog.Default().Enabled(context.Background(), slog.LevelError) {
		t.Error("error should be enabled at warn level")
	}

	// The level var is live: lowering it takes effect on the already-built handler.
	config.LogLevel.Set(slog.LevelDebug)
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		t.Error("debug should be enabled after lowering the level")
	}
}

func TestInitJSONFormat(t *testing.T) {
	config.LogFormat = "json"
	config.LogLevel.Set(slog.LevelInfo)
	Init() // smoke: must not panic with the json handler
}
