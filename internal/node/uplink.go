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

// ConnectGateway maintains an outbound uplink to the gateway at url until ctx is
// cancelled, reconnecting with capped exponential backoff. token authenticates
// as a node; httpClient may carry a TLS config (nil uses the default).
//
// Over the uplink the node is a symmetric peer: it serves the gateway's control
// requests through the same handler registry as local clients, and pushes its
// registry changes up as session.event notifications. The gateway pulls the initial
// snapshot via sessions.list, so only live events are streamed here.
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

// runUplink dials the gateway and pumps events until the connection or ctx ends. It
// returns whether the dial succeeded (to drive backoff reset).
func (d *Node) runUplink(ctx context.Context, url, token string, httpClient *http.Client) (connected bool) {
	peer, err := api.DialWSPeer(ctx, url, token, httpClient, api.PeerOptions{
		// The gateway issues control requests (capture/input/respond/...) down this
		// link; serve them through the same handlers local clients use.
		Dispatch: d.server.DispatchFunc(),
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
