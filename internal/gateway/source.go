// Package gateway maintains a blind roster of node sources (metadata + online/offline liveness)
// and relays opaque frames between clients and nodes; it does NOT see session data.
package gateway

import (
	"github.com/MunifTanjim/argus/internal/api"
)

// Source is a node registered in the roster, providing blind metadata (id, label, version,
// capabilities, keys) and a Done() liveness signal; it does not carry session data.
type Source interface {
	// ID is the stable node identifier (the composite-id prefix and routing key).
	ID() string
	// Label is a human-friendly name, e.g. the hostname.
	Label() string
	// Version is the node's binary version.
	Version() string
	// Capabilities reports what the node supports (e.g. spawn = tmux present).
	Capabilities() api.NodeCapabilities
	// IdentityPubKey is the node's Noise static public key (base64), for E2E channel
	// setup. Empty when the node has no key (pre-E2E / co-located).
	IdentityPubKey() string
	// SignerPubKey is the node's Ed25519 signer public key (base64), advertised so a
	// future `lock init` can designate it as a trusted signer. Empty when unset.
	SignerPubKey() string
	// Done is closed when the source disconnects (never fires for the in-process source).
	Done() <-chan struct{}
}

// InProcessSource adapts the local engine to the Source interface, with no
// serialization or network hop.
type InProcessSource struct {
	id, label, version string
	identityPubKey     string
	signerPubKey       string
	caps               api.NodeCapabilities
	done               chan struct{} // never closed: the local engine is always present
}

// NewInProcessSource wraps a local node as a Source.
func NewInProcessSource(id, label, version, identityPubKey, signerPubKey string, caps api.NodeCapabilities) *InProcessSource {
	return &InProcessSource{id: id, label: label, version: version, identityPubKey: identityPubKey, signerPubKey: signerPubKey, caps: caps, done: make(chan struct{})}
}

func (s *InProcessSource) ID() string                         { return s.id }
func (s *InProcessSource) Label() string                      { return s.label }
func (s *InProcessSource) Version() string                    { return s.version }
func (s *InProcessSource) IdentityPubKey() string             { return s.identityPubKey }
func (s *InProcessSource) SignerPubKey() string               { return s.signerPubKey }
func (s *InProcessSource) Capabilities() api.NodeCapabilities { return s.caps }
func (s *InProcessSource) Done() <-chan struct{}              { return s.done }
