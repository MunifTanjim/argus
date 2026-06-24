package config

import "log/slog"

// LogLevel is the global logging threshold, a *slog.LevelVar so the logger's handler
// reflects changes live. It defaults to Info and is set from the resolved config at
// startup (see logger.Init); the CLI flag can also retune it at runtime.
var LogLevel = new(slog.LevelVar) // zero value is LevelInfo

// LogFormat selects the log handler: "pretty" (default) or "json". It is set from the
// resolved config at startup, before the handler is built.
var LogFormat = "pretty"
