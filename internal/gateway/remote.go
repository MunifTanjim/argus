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
	caps               api.NodeCapabilities
	peer               *api.Peer
}

// NewRemoteSource wraps an accepted node uplink as a Source.
func NewRemoteSource(id, label, version, identityPubKey, signerPubKey string, caps api.NodeCapabilities, peer *api.Peer) *RemoteSource {
	return &RemoteSource{id: id, label: label, version: version, identityPubKey: identityPubKey, signerPubKey: signerPubKey, caps: caps, peer: peer}
}

func (r *RemoteSource) ID() string                         { return r.id }
func (r *RemoteSource) Label() string                      { return r.label }
func (r *RemoteSource) Version() string                    { return r.version }
func (r *RemoteSource) IdentityPubKey() string             { return r.identityPubKey }
func (r *RemoteSource) SignerPubKey() string               { return r.signerPubKey }
func (r *RemoteSource) Capabilities() api.NodeCapabilities { return r.caps }
func (r *RemoteSource) Done() <-chan struct{}              { return r.peer.Done() }
