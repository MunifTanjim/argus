package node

import (
	"cmp"
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

// uplinkDispatch is the cleartext gateway→node request surface over the uplink.
// It answers only node.identify; all other requests are refused. Gateway→node
// notifications (not requests) are handled via OnNotify, not here.
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
// The node answers no gateway→node commands over the cleartext uplink; it
// accepts one notification (trustlog.changed) as an untrusted pull hint.
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
		// No gateway→node commands are answered over the cleartext uplink; clients
		// reach node handlers only through the E2E responder. One hint is accepted:
		// trustlog.changed triggers a pull the node already does on a timer, so a
		// forged or withheld notification changes only when the pull happens, not what
		// the node accepts (verified against pinned genesis either way).
		Dispatch: d.uplinkDispatch(),
		OnNotify: d.onGatewayNotify,
		// Relayed E2E frames from clients are terminated by the responder.
		OnRelayFrame: resp.onFrame,
		// The gateway heartbeats this link from its side, but that only lets the
		// gateway notice a half-open uplink. Ping from here too so the node also
		// detects one and re-dials instead of sitting on a dead connection.
		KeepaliveInterval:         cmp.Or(d.keepaliveInterval, api.DefaultKeepaliveInterval),
		KeepaliveTimeout:          api.DefaultKeepaliveTimeout,
		KeepaliveFailureThreshold: api.DefaultKeepaliveFailures,
		Logger:                    d.log,
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
