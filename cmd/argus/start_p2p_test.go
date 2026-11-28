package main

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"

	"github.com/MunifTanjim/argus/internal/config"
)

// lateTailnet is a tailscaled that is down until up is set.
type lateTailnet struct{ up atomic.Bool }

func (l *lateTailnet) WhoIs(context.Context, string) (*apitype.WhoIsResponse, error) {
	return nil, errors.New("down")
}

func (l *lateTailnet) Status(context.Context) (*ipnstate.Status, error) {
	if !l.up.Load() {
		return nil, errors.New("tailscaled not running")
	}
	return &ipnstate.Status{Self: &ipnstate.PeerStatus{TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}}}, nil
}

// The endpoint source asks tailscaled again on every call, so endpoints
// missing at startup appear once tailscaled is up.
func TestP2PEndpointSourceRecomputes(t *testing.T) {
	cfg := &config.Config{}
	cfg.P2P.Listen = []string{"100.64.0.1:7777"}
	ts := &lateTailnet{}
	src := p2pEndpointSource(cfg, ts)
	if got := src(context.Background()); len(got) != 0 {
		t.Fatalf("endpoints before tailscaled is up = %v, want none", got)
	}
	ts.up.Store(true)
	if got := src(context.Background()); len(got) != 1 || got[0] != "ws://100.64.0.1:7777/p2p" {
		t.Fatalf("endpoints = %v, want ws://100.64.0.1:7777/p2p", got)
	}
}
