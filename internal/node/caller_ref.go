package node

import (
	"context"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// callerRef is the actor behind an orch.* call. Local is the caller's session.
type callerRef struct {
	Actor     api.Actor
	SessionID string // composite
	Label     string
	Local     *session.Session
}

func (d *Node) localCallerRef(s session.Session) callerRef {
	return callerRef{Actor: d.actor(s), SessionID: d.compositeID(s), Label: sessionLabel(s), Local: &s}
}

// callerFrom identifies the caller of a request.
func (d *Node) callerFrom(ctx context.Context, c api.CallerInfo) (callerRef, error) {
	s, err := d.resolveCaller(ctx, c)
	if err != nil {
		return callerRef{}, err
	}
	return d.localCallerRef(s), nil
}
