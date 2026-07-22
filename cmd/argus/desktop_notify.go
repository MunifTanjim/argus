package main

import (
	"github.com/MunifTanjim/argus/internal/config"
)

// desktopClickCmd builds the command a notification click runs: this binary's
// hidden `_focus` subcommand against the local node socket, carrying the session id.
func desktopClickCmd(cfg *config.Config) func(string) []string {
	bin := detectArgusBin()
	socket := cfg.Socket
	return func(sessionID string) []string {
		return []string{bin, "_focus", "--socket", socket, sessionID}
	}
}
