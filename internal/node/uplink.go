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

// runUplink returns whether the dial succeeded, to drive backoff reset. The node runs a
// relay responder that terminates channels from clients; the gateway can only
// issue node.identify, all client requests travel through relay frames.
func (d *Node) runUplink(ctx context.Context, url, token string, httpClient *http.Client) (connected bool) {
	if d.p2pSource != nil {
		d.SetP2PEndpoints(d.p2pSource(ctx))
	}
	pool := d.newPeerPool()
	resp := d.newRelayResponder(pool)
	peer, err := api.DialWSPeer(ctx, url, token, httpClient, api.PeerOptions{
		Dispatch:     d.uplinkDispatch(),
		OnNotify:     d.uplinkNotify(resp),
		OnRelayFrame: resp.onFrame,
	})
	if err != nil {
		pool.closeAll()
		if ctx.Err() == nil {
			d.log.Warn("gateway uplink dial failed", "url", url, "err", err)
		}
		return false
	}
	resp.peer.Store(peer)
	pool.peer.Store(peer)
	d.activeResponder.Store(resp)
	d.activeUplink.Store(peer)
	d.peers.Store(pool)
	pool.watchRoster()
	defer func() {
		d.activeUplink.CompareAndSwap(peer, nil)
		d.activeResponder.CompareAndSwap(resp, nil)
		d.peers.CompareAndSwap(pool, nil)
		// Direct links need a live uplink. bindings() is nil from here, so no
		// handshake can admit with this uplink's store once these are closed.
		d.closeAllDirect()
		pool.closeAll()
		resp.closeAll()
		peer.Close()
		// Reset only once the read loop has exited, so a late node.event cannot
		// repopulate the roster of a closed uplink.
		<-peer.Done()
		d.roster.reset()
	}()
	d.log.Info("gateway uplink established", "url", url)
	// Wire the push egress for this uplink unconditionally. The store gate is
	// enforced at delivery time (handlePushTest / StartPush); the store may be set
	// after this uplink connects, so guarding on it here would leave a stably
	// connected node with a nil deliverer until an unrelated reconnect.
	d.SetPushDeliverer(uplinkDeliverer{peer: peer})

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
