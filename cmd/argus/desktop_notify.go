package main

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/push"
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

// fanouter is the subset of *gateway.Aggregator the broadcaster needs (kept small
// for testability).
type fanouter interface {
	Fanout(ctx context.Context, method string, params json.RawMessage) []gateway.FanoutResult
}

// fanoutNotifier is a push.Sink that broadcasts a desktop notification to every
// connected node via the push.desktop RPC; each node renders it only if opted in
// (see Node.handlePushDesktop). Lives in cmd/argus, not internal/push, to avoid an
// import cycle (internal/gateway already imports internal/push).
type fanoutNotifier struct {
	agg fanouter
	log *slog.Logger
}

func (f fanoutNotifier) Notify(ctx context.Context, n push.Notification) {
	log := f.log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	b, err := json.Marshal(n)
	if err != nil {
		log.Warn("push: marshal desktop notification", "err", err)
		return
	}
	f.agg.Fanout(ctx, api.MethodPushDesktop, b)
}
