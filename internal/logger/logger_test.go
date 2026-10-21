package logger

import (
	"bytes"
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

func TestColorEnabled(t *testing.T) {
	env := func(kv map[string]string) func(string) string {
		return func(k string) string { return kv[k] }
	}
	cases := []struct {
		name string
		tty  bool
		env  map[string]string
		want bool
	}{
		{"tty", true, nil, true},
		{"pipe", false, nil, false},
		{"pipe with CLICOLOR_FORCE", false, map[string]string{"CLICOLOR_FORCE": "1"}, true},
		{"CLICOLOR_FORCE=0", false, map[string]string{"CLICOLOR_FORCE": "0"}, false},
		{"NO_COLOR on a tty", true, map[string]string{"NO_COLOR": "1"}, false},
		{"NO_COLOR wins over CLICOLOR_FORCE", false, map[string]string{"NO_COLOR": "1", "CLICOLOR_FORCE": "1"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := colorEnabled(tc.tty, env(tc.env)); got != tc.want {
				t.Fatalf("colorEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPrettyHandlerNoColorWritesNoEscapes(t *testing.T) {
	config.LogLevel.Set(slog.LevelInfo)
	var buf bytes.Buffer
	slog.New(prettyHandler(&buf, true)).Info("hello", "scope", "node", "k", "v")
	if bytes.IndexByte(buf.Bytes(), 0x1b) >= 0 {
		t.Fatalf("output has ANSI escapes: %q", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("node")) {
		t.Fatalf("output lacks the scope prefix: %q", buf.String())
	}
}

func TestInitWriterWritesToWriterWithoutColor(t *testing.T) {
	prevLogger, prevFormat, prevLevel := slog.Default(), config.LogFormat, config.LogLevel.Level()
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		config.LogFormat = prevFormat
		config.LogLevel.Set(prevLevel)
	})
	config.LogLevel.Set(slog.LevelInfo)
	config.LogFormat = "pretty"
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")
	var buf bytes.Buffer
	InitWriter(&buf)
	slog.Info("hello")
	if !bytes.Contains(buf.Bytes(), []byte("hello")) || bytes.Contains(buf.Bytes(), []byte("\x1b[")) {
		t.Fatalf("output = %q", buf.String())
	}
}
