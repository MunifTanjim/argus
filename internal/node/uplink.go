package node

import (
	"context"
	"net/http"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
)

const (
	uplinkBaseBackoff = 500 * time.Millisecond
	uplinkMaxBackoff  = 15 * time.Second
)

// ConnectGateway maintains an outbound uplink to the gateway until ctx is
// cancelled, reconnecting with capped exponential backoff. nil httpClient uses
// the default.
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
	if d.e2ee {
		return d.runUplinkBlind(ctx, url, token, httpClient)
	}
	return d.runUplinkPlain(ctx, url, token, httpClient)
}

// runUplinkPlain is the original plaintext uplink: the gateway issues control
// requests down this link and the node pushes registry changes up as session.event.
func (d *Node) runUplinkPlain(ctx context.Context, url, token string, httpClient *http.Client) (connected bool) {
	peer, err := api.DialWSPeer(ctx, url, token, httpClient, api.PeerOptions{
		// The gateway issues control requests (capture/input/respond/...) down this
		// link. remoteDispatch filters lock.* so lock handlers are never reachable
		// from the network — local unix socket only.
		Dispatch: d.remoteDispatch(),
	})
	if err != nil {
		if ctx.Err() == nil {
			d.log.Warn("gateway uplink dial failed", "url", url, "err", err)
		}
		return false
	}
	defer peer.Close()
	d.log.Info("gateway uplink established", "url", url)

	// Subscribe before the gateway pulls our snapshot so no live event is lost.
	events, cancel := d.reg.Subscribe()
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return true
		case <-peer.Done():
		case ev, ok := <-events:
			if !ok {
				return true
			}
			if err := peer.Notify(api.MethodSessionEvent, ev); err == nil {
				continue
			}
		}
		// peer.Done() fired, or a Notify failed: the uplink is gone. Log once,
		// but not on clean shutdown (cancellation).
		if ctx.Err() == nil {
			d.log.Info("gateway uplink closed", "url", url)
		}
		return true
	}
}

// runUplinkBlind is the E2E blind uplink: the node runs a relay responder that
// terminates Noise IK channels from clients. The gateway can only issue
// node.identify; all client requests travel sealed end-to-end.
func (d *Node) runUplinkBlind(ctx context.Context, url, token string, httpClient *http.Client) (connected bool) {
	resp := d.newRelayResponder()
	peer, err := api.DialWSPeer(ctx, url, token, httpClient, api.PeerOptions{
		Dispatch:     d.uplinkDispatch(),
		OnNotify:     d.onGatewayNotify,
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
	if d.pushStore != nil {
		d.SetPushDeliverer(uplinkDeliverer{peer: peer})
	}

	// Sync the trust-log chain over this uplink (no-op unless locked mode is on).
	go d.runTrustSync(ctx, peer)

	select {
	case <-ctx.Done():
	case <-peer.Done():
		if ctx.Err() == nil {
			d.log.Info("gateway uplink closed", "url", url)
		}
	}
	return true
}
