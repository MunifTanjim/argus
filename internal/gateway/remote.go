package gateway

import (
	"github.com/MunifTanjim/argus/internal/api"
)

// RemoteSource is a node reached over the WebSocket uplink, adapted to Source.
type RemoteSource struct {
	id, label, version string
	identityPubKey     string
	signerPubKey       string
	lockDisabled       bool
	caps               api.NodeCapabilities
	peer               *api.Peer
}

func NewRemoteSource(id api.IdentifyResult, peer *api.Peer) *RemoteSource {
	return &RemoteSource{
		id: id.ID, label: id.Label, version: id.Version,
		identityPubKey: id.IdentityPubKey, signerPubKey: id.SignerPubKey,
		lockDisabled: id.LockDisabled,
		caps:         id.Capabilities, peer: peer,
	}
}

func (r *RemoteSource) ID() string                         { return r.id }
func (r *RemoteSource) Label() string                      { return r.label }
func (r *RemoteSource) Version() string                    { return r.version }
func (r *RemoteSource) Capabilities() api.NodeCapabilities { return r.caps }
func (r *RemoteSource) IdentityPubKey() string             { return r.identityPubKey }
func (r *RemoteSource) SignerPubKey() string               { return r.signerPubKey }
func (r *RemoteSource) LockDisabled() bool                 { return r.lockDisabled }

func (r *RemoteSource) Done() <-chan struct{} { return r.peer.Done() }
