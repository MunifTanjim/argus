package gateway

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// RemoteSource is a node reached over the WebSocket uplink, adapted to Source.
// It carries both plaintext session machinery (events channel, Snapshot/Subscribe/Call)
// and blind-path metadata (identity/signer/beacon pubkeys, latest beacon).
type RemoteSource struct {
	id, label, version string
	identityPubKey     string
	signerPubKey       string
	beaconPubKey       string
	latestBeacon       *api.Beacon
	caps               api.NodeCapabilities
	peer               *api.Peer
	events             <-chan registry.Event // plaintext path: decoded session.event notifications
}

// NewRemoteSource wraps an accepted node uplink as a Source.
// events is nil by default; call withEvents to set it for the plaintext aggregator path.
func NewRemoteSource(id, label, version, identityPubKey, signerPubKey, beaconPubKey string, caps api.NodeCapabilities, peer *api.Peer, beacon *api.Beacon) *RemoteSource {
	return &RemoteSource{
		id: id, label: label, version: version,
		identityPubKey: identityPubKey, signerPubKey: signerPubKey, beaconPubKey: beaconPubKey,
		latestBeacon: beacon, caps: caps, peer: peer,
	}
}

// withEvents sets the session event channel for the plaintext aggregator path and returns r.
func (r *RemoteSource) withEvents(ch <-chan registry.Event) *RemoteSource {
	r.events = ch
	return r
}

func (r *RemoteSource) ID() string                         { return r.id }
func (r *RemoteSource) Label() string                      { return r.label }
func (r *RemoteSource) Version() string                    { return r.version }
func (r *RemoteSource) Capabilities() api.NodeCapabilities { return r.caps }
func (r *RemoteSource) IdentityPubKey() string             { return r.identityPubKey }
func (r *RemoteSource) SignerPubKey() string               { return r.signerPubKey }
func (r *RemoteSource) BeaconPubKey() string               { return r.beaconPubKey }
func (r *RemoteSource) LatestBeacon() *api.Beacon          { return r.latestBeacon }

// Snapshot pulls the node's current sessions via sessions.list.
func (r *RemoteSource) Snapshot() []session.Session {
	var out []session.Session
	_ = r.peer.Call(api.MethodSessionsList, nil, &out)
	return out
}

func (r *RemoteSource) Subscribe() (<-chan registry.Event, func()) {
	return r.events, func() {}
}

// Call forwards a control request to the node. The context bounds the wait so a
// wedged node can't block the caller past its deadline.
func (r *RemoteSource) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	if err := r.peer.CallContext(ctx, method, params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *RemoteSource) Done() <-chan struct{} { return r.peer.Done() }
