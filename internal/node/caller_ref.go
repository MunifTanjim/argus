package node

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	Actor     api.Actor
	SessionID string // composite
	Label     string
	Local     *session.Session
}

func (d *Node) localCallerRef(s session.Session) callerRef {
	return callerRef{Actor: d.actor(s), SessionID: d.compositeID(s), Label: sessionLabel(s), Local: &s}
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
		if sid := c.Remote.SessionID; sid != "" && !strings.HasPrefix(sid, peer+":") {
			return callerRef{}, fmt.Errorf("%w: session %s is not on node %s", errRemoteCallerDenied, sid, peer)
		}
		return callerRef{Actor: c.Remote.Actor, SessionID: c.Remote.SessionID, Label: c.Remote.Label}, nil
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
