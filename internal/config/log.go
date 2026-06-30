package config

import "log/slog"

// LogLevel is the global logging threshold; a *slog.LevelVar so the handler reflects
// changes live (set from config at startup, retunable via CLI flag at runtime).
var LogLevel = new(slog.LevelVar) // zero value is LevelInfo

// LogFormat selects the log handler: "pretty" (default) or "json".
var LogFormat = "pretty"
