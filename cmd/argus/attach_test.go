package main

import (
	"bytes"
	"testing"

	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/trustpin"
)

func TestDecideAttach(t *testing.T) {
	cases := []struct {
		name          string
		st            *launchState
		node, gateway bool
		want          attachAction
	}{
		{"no state, nothing", nil, false, false, attachNone},
		{"no state, node", nil, true, false, attachNone},
		{"no state, standalone gateway", nil, false, true, attachNone},
		{"isolated, node", &launchState{Mode: launchModeIsolated}, true, false, attachState},
		{"isolated, standalone gateway", &launchState{Mode: launchModeIsolated}, true, true, attachState},
		{"isolated, gone", &launchState{Mode: launchModeIsolated}, false, false, attachNone},
		{"connected, node", &launchState{Mode: launchModeConnected}, true, false, attachState},
		{"gateway, both", &launchState{Mode: launchModeGateway}, true, true, attachState},
		{"gateway, only gateway", &launchState{Mode: launchModeGateway}, false, true, attachPartial},
		{"gateway, only node", &launchState{Mode: launchModeGateway}, true, false, attachPartial},
		{"gateway, gone", &launchState{Mode: launchModeGateway}, false, false, attachNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decideAttach(c.st, c.node, c.gateway); got != c.want {
				t.Errorf("decideAttach = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAttachTarget(t *testing.T) {
	sandboxChild(t)
	genesis := bytes.Repeat([]byte{7}, 32)
	cfg := &config.Config{}
	cfg.Lock.Genesis = trustpin.Encode(genesis)
	head := []byte("client head")
	cases := []struct {
		name     string
		st       launchState
		url, tok string
		wantHead []byte
	}{
		{"isolated", launchState{Mode: launchModeIsolated}, "", "", nil},
		{"connected", launchState{Mode: launchModeConnected, GatewayURL: "ws://gw", Token: "t"}, "ws://gw", "t", head},
		{"gateway", launchState{Mode: launchModeGateway, GatewayURL: "ws://127.0.0.1:1", Token: "g"}, "ws://127.0.0.1:1", "g", genesis},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url, tok, h, err := attachTarget(cfg, &c.st, head)
			if err != nil || url != c.url || tok != c.tok || !bytes.Equal(h, c.wantHead) {
				t.Errorf("attachTarget = (%q, %q, %x, %v), want (%q, %q, %x)", url, tok, h, err, c.url, c.tok, c.wantHead)
			}
		})
	}
}
