// Package gateway aggregates sessions from many node sources into one merged view
// and routes control calls back to the owning node. A source is either the local
// engine (in-process) or a remote node over the WebSocket uplink; both implement Source.
package gateway

import (
	"context"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// Source is one node feeding the aggregator. Its session ids are node-local;
// the aggregator namespaces them into composite ids.
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
	// Snapshot returns the source's current sessions (node-local ids).
	Snapshot() []session.Session
	// Subscribe returns the source's live event stream and a cancel function.
	Subscribe() (<-chan registry.Event, func())
	// Call invokes a control method with already node-local params, returning raw JSON.
	Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
	// Done is closed when the source disconnects (never fires for the in-process source).
	Done() <-chan struct{}
}

// InProcessSource adapts the local engine (a registry plus a control dispatcher)
// to the Source interface, with no serialization or network hop.
type InProcessSource struct {
	id, label, version string
	identityPubKey     string
	caps               api.NodeCapabilities
	reg                *registry.Registry
	dispatch           api.DispatchFunc
	done               chan struct{} // never closed: the local engine is always present
}

// NewInProcessSource wraps a local registry and control dispatch as a Source.
func NewInProcessSource(id, label, version, identityPubKey string, caps api.NodeCapabilities, reg *registry.Registry, dispatch api.DispatchFunc) *InProcessSource {
	return &InProcessSource{id: id, label: label, version: version, identityPubKey: identityPubKey, caps: caps, reg: reg, dispatch: dispatch, done: make(chan struct{})}
}

func (s *InProcessSource) ID() string                                 { return s.id }
func (s *InProcessSource) Label() string                              { return s.label }
func (s *InProcessSource) Version() string                            { return s.version }
func (s *InProcessSource) IdentityPubKey() string                     { return s.identityPubKey }
func (s *InProcessSource) Capabilities() api.NodeCapabilities         { return s.caps }
func (s *InProcessSource) Snapshot() []session.Session                { return s.reg.Snapshot() }
func (s *InProcessSource) Subscribe() (<-chan registry.Event, func()) { return s.reg.Subscribe() }
func (s *InProcessSource) Done() <-chan struct{}                      { return s.done }

// Call dispatches to the local handlers and marshals the result.
func (s *InProcessSource) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	res, err := s.dispatch(ctx, method, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(res)
}
