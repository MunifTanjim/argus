package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// refuseAll is a dispatch that serves nothing — the shape of a deliberately
// narrow control surface (e.g. the node's cleartext uplink, which answers only
// node.identify).
func refuseAll(_ context.Context, method string, _ json.RawMessage) (any, error) {
	return nil, &RPCError{Code: CodeMethodNotFound, Message: "method not found: " + method}
}

// ping is a transport-level liveness probe, so it must be answered by the Peer
// itself, before any application dispatch gets a say. A narrow dispatch that
// refuses unknown methods must not be able to break its own link's keepalive.
func TestPeerAnswersPingRegardlessOfDispatch(t *testing.T) {
	pa, _, done := makePeers(
		PeerOptions{},
		PeerOptions{Dispatch: refuseAll},
	)
	defer done()

	if err := pa.Call(MethodPing, nil, nil); err != nil {
		t.Fatalf("ping must be answered even when dispatch refuses everything: %v", err)
	}
}

// A peer with no dispatch at all (a pure consumer, e.g. api.Client) still answers
// ping, so a server can heartbeat its clients.
func TestPeerAnswersPingWithNilDispatch(t *testing.T) {
	pa, _, done := makePeers(PeerOptions{}, PeerOptions{})
	defer done()

	if err := pa.Call(MethodPing, nil, nil); err != nil {
		t.Fatalf("ping must be answered with a nil dispatch: %v", err)
	}
}

// Regression: the gateway heartbeats node uplinks, and the node's uplink dispatch
// serves only node.identify. Keepalive must not tear down that link.
func TestPeerKeepaliveSurvivesRestrictiveDispatch(t *testing.T) {
	pa, _, done := makePeers(
		PeerOptions{KeepaliveInterval: 20 * time.Millisecond, KeepaliveTimeout: 40 * time.Millisecond, KeepaliveFailureThreshold: 2},
		PeerOptions{Dispatch: refuseAll},
	)
	defer done()

	select {
	case <-pa.Done():
		t.Fatal("keepalive tore down a live link whose remote serves a narrow dispatch")
	case <-time.After(200 * time.Millisecond):
		// good: the link survived several keepalive cycles
	}
}

// An RPC-level error reply still proves the remote is alive and processing
// frames, so it must reset the keepalive failure streak rather than count as a
// missed ping.
func TestPeerKeepaliveCountsRPCErrorAsAlive(t *testing.T) {
	pa, _, done := makePeers(
		PeerOptions{KeepaliveInterval: 20 * time.Millisecond, KeepaliveTimeout: 40 * time.Millisecond},
		PeerOptions{Dispatch: func(context.Context, string, json.RawMessage) (any, error) {
			return nil, &RPCError{Code: CodeInternalError, Message: "boom"}
		}},
	)
	defer done()

	select {
	case <-pa.Done():
		t.Fatal("keepalive closed a peer that answered with an RPC error")
	case <-time.After(150 * time.Millisecond):
		// good: a reply is a reply
	}
}
