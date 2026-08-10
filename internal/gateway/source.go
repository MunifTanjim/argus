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
	// BeaconPubKey is the node's Ed25519 beacon public key (base64), for
	// anti-equivocation beacon verification. Empty when unset.
	BeaconPubKey() string
	// LatestBeacon is the initial signed HEAD beacon from the node's identify call
	// (nil when the node has no beacon key or the beacon is unavailable).
	LatestBeacon() *api.Beacon
	// Done is closed when the source disconnects.
	Done() <-chan struct{}
}
