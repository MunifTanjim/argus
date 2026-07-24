package node

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

const (
	uplinkBaseBackoff = 500 * time.Millisecond
	uplinkMaxBackoff  = 15 * time.Second
)

// uplinkDispatch is the cleartext gateway→node control surface over the uplink.
// After the blind-gateway cutover the gateway issues no session control here —
// clients reach node handlers only through the E2E responder — so this answers
// just node.identify and refuses everything else, keeping the node from serving
// cleartext session data up the uplink.
func (d *Node) uplinkDispatch() api.DispatchFunc {
	full := d.remoteDispatch()
	return func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method == api.MethodNodeIdentify {
			return full(ctx, method, params)
		}
		return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found: " + method}
	}
}

// ConnectGateway maintains an outbound uplink to the gateway until ctx is
// cancelled, reconnecting with capped exponential backoff. nil httpClient uses
// the default.
//
// The node is a symmetric peer: it serves only node.identify over the cleartext
// uplink; client control flows exclusively through the E2E responder.
func (d *Node) ConnectGateway(ctx context.Context, url, token string, httpClient *http.Client) {
	d.log.Info("connecting to gateway", "url", url)
	backoff := uplinkBaseBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		connected := d.runUplink(ctx, url, token, httpClient)
		if ctx.Err() != nil {
			return
		}
		if connected {
			backoff = uplinkBaseBackoff // reset after a successful session
		}
		d.log.Debug("retrying gateway uplink", "backoff", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > uplinkMaxBackoff {
			backoff = uplinkMaxBackoff
		}
	}
}

// runUplink dials the gateway and waits until the connection or ctx ends. It
// returns whether the dial succeeded (to drive backoff reset).
func (d *Node) runUplink(ctx context.Context, url, token string, httpClient *http.Client) (connected bool) {
	resp := d.newRelayResponder()
	peer, err := api.DialWSPeer(ctx, url, token, httpClient, api.PeerOptions{
		// Only node.identify is answered over the cleartext uplink; all other
		// gateway→node control is refused (clients go through the E2E responder).
		Dispatch: d.uplinkDispatch(),
		// Relayed E2E frames from clients are terminated by the responder.
		OnRelayFrame: resp.onFrame,
	})
	if err != nil {
		if ctx.Err() == nil {
			d.log.Warn("gateway uplink dial failed", "url", url, "err", err)
		}
		return false
	}
	resp.peer.Store(peer)
	d.activeResponder.Store(resp)
	d.activeUplink.Store(peer)
	defer peer.Close()
	defer resp.closeAll()
	defer d.activeResponder.CompareAndSwap(resp, nil)
	defer d.activeUplink.CompareAndSwap(peer, nil)
	d.log.Info("gateway uplink established", "url", url)

	// Sync the trust-log chain over this uplink (no-op unless locked mode is on).
	go d.runTrustSync(ctx, peer)

	// Deliver encrypted mobile pushes over this uplink; desktop renders node-local.
	if d.pushStore != nil {
		d.SetPushDeliverer(uplinkDeliverer{peer: peer})
	}

	// Wait until the uplink or context ends; no session events are pushed here —
	// clients are now blind-gateway E2E only.
	select {
	case <-ctx.Done():
	case <-peer.Done():
		// peer.Done() fired: the uplink is gone. Log once, but not on clean shutdown.
		if ctx.Err() == nil {
			d.log.Info("gateway uplink closed", "url", url)
		}
	}
	return true
}
