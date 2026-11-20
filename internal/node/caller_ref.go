package node

import (
	"context"
	"errors"
	"fmt"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

var (
	errRemoteCallerDenied = errors.New("remote caller not allowed on this connection")
	errLocalCallerOnPeer  = errors.New("a peer node must send a remote caller")
)

// callerRef is the actor behind an orch.* call. Local is the caller's session
// when it runs on this node, and nil for a caller on another node.
type callerRef struct {
	Actor api.Actor
	Label string
	Local *session.Session
}

func (d *Node) localCallerRef(s session.Session) callerRef {
	return callerRef{Actor: d.actor(s), Label: sessionLabel(s), Local: &s}
}

// callerFrom identifies the caller of a request. A remote caller is accepted
// only from the peer channel of its own node, and a peer channel never resolves
// a caller from this machine's processes.
func (d *Node) callerFrom(ctx context.Context, c api.CallerInfo) (callerRef, error) {
	peer, onPeer := peerNodeFrom(ctx)
	if c.Remote != nil {
		if !onPeer || peer != c.Remote.Actor.Node {
			return callerRef{}, errRemoteCallerDenied
		}
		if k := c.Remote.Actor.Key; reservedKey(k) {
			return callerRef{}, fmt.Errorf("%w: %s is a reserved actor", errRemoteCallerDenied, k)
		}
		return callerRef{Actor: c.Remote.Actor, Label: c.Remote.Label}, nil
	}
	if onPeer {
		return callerRef{}, errLocalCallerOnPeer
	}
	s, err := d.resolveCaller(ctx, c)
	if err != nil {
		return callerRef{}, err
	}
	return d.localCallerRef(s), nil
}

// name is how errors name the caller: its agent ID, else its label.
func (c callerRef) name() string {
	if id := actorID(c.Actor); id != "" {
		return id
	}
	return c.Label
}
