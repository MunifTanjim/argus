package gateway

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// RemoteSource is a node reached over the WebSocket uplink, adapted to the
// Source interface via the symmetric Peer: snapshots and control calls are
// requests the gateway issues down the link; live events arrive as the node's
// session.event notifications (decoded into the events channel by the caller).
type RemoteSource struct {
	id, label string
	caps      api.NodeCapabilities
	peer      *api.Peer
	events    <-chan registry.Event
}

// NewRemoteSource wraps an accepted node uplink. events must be the channel the
// peer's OnNotify decodes session.event notifications into.
func NewRemoteSource(id, label string, caps api.NodeCapabilities, peer *api.Peer, events <-chan registry.Event) *RemoteSource {
	return &RemoteSource{id: id, label: label, caps: caps, peer: peer, events: events}
}

func (r *RemoteSource) ID() string                         { return r.id }
func (r *RemoteSource) Label() string                      { return r.label }
func (r *RemoteSource) Capabilities() api.NodeCapabilities { return r.caps }

// Snapshot pulls the node's current sessions via sessions.list.
func (r *RemoteSource) Snapshot() []session.Session {
	var out []session.Session
	_ = r.peer.Call(api.MethodSessionsList, nil, &out)
	return out
}

func (r *RemoteSource) Subscribe() (<-chan registry.Event, func()) {
	return r.events, func() {}
}

// Call forwards a control request to the node and returns the raw JSON result.
// The context bounds the wait so a wedged node can't block the caller (e.g. a
// gateway Fanout) past its deadline.
func (r *RemoteSource) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	if err := r.peer.CallContext(ctx, method, params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *RemoteSource) Done() <-chan struct{} { return r.peer.Done() }
