// Package gateway tracks roster liveness across node sources and exposes
// a relay-based server for client connections.
package gateway

import (
	"github.com/MunifTanjim/argus/internal/api"
)

// Source is one node feeding the aggregator. The aggregator uses only the
// identity/liveness subset.
type Source interface {
	// ID is the stable node identifier.
	ID() string
	// Label is a human-friendly name, e.g. the hostname.
	Label() string
	// Version is the node's binary version.
	Version() string
	// Capabilities reports what the node supports.
	Capabilities() api.NodeCapabilities
	// IdentityPubKey is the node's Noise static public key (base64). Empty when unset.
	IdentityPubKey() string
	// SignerPubKey is the node's Ed25519 signer public key (base64). Empty when unset.
	SignerPubKey() string
	// Done is closed when the source disconnects.
	Done() <-chan struct{}
}
