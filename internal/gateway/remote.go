package gateway

import (
	"github.com/MunifTanjim/argus/internal/api"
)

// RemoteSource is a node reached over the WebSocket uplink, adapted to Source.
// The gateway is blind to sessions: it relays opaque E2E frames and tracks only
// the node's liveness (online/offline/removed).
type RemoteSource struct {
	id, label, version string
	identityPubKey     string
	signerPubKey       string
	beaconPubKey       string
	latestBeacon       *api.Beacon // initial beacon from node.identify; subsequent updates go via beacon.offer
	caps               api.NodeCapabilities
	peer               *api.Peer
}

// NewRemoteSource wraps an accepted node uplink as a Source.
func NewRemoteSource(id, label, version, identityPubKey, signerPubKey, beaconPubKey string, caps api.NodeCapabilities, peer *api.Peer, beacon *api.Beacon) *RemoteSource {
	return &RemoteSource{id: id, label: label, version: version, identityPubKey: identityPubKey, signerPubKey: signerPubKey, beaconPubKey: beaconPubKey, latestBeacon: beacon, caps: caps, peer: peer}
}

func (r *RemoteSource) ID() string                         { return r.id }
func (r *RemoteSource) Label() string                      { return r.label }
func (r *RemoteSource) Version() string                    { return r.version }
func (r *RemoteSource) IdentityPubKey() string             { return r.identityPubKey }
func (r *RemoteSource) SignerPubKey() string               { return r.signerPubKey }
func (r *RemoteSource) BeaconPubKey() string               { return r.beaconPubKey }
func (r *RemoteSource) LatestBeacon() *api.Beacon          { return r.latestBeacon }
func (r *RemoteSource) Capabilities() api.NodeCapabilities { return r.caps }
func (r *RemoteSource) Done() <-chan struct{}              { return r.peer.Done() }
