package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/orch"
)

// requestLocks serializes the calls that share a caller actor and a request_id.
type requestLocks struct {
	mu   sync.Mutex
	held map[string]*requestLock
}

type requestLock struct {
	mu   sync.Mutex
	refs int
}

func (r *requestLocks) lock(actor api.Actor, requestID string) (unlock func()) {
	key := actor.Node + "\x00" + actor.Key + "\x00" + requestID
	r.mu.Lock()
	if r.held == nil {
		r.held = map[string]*requestLock{}
	}
	l, ok := r.held[key]
	if !ok {
		l = &requestLock{}
		r.held[key] = l
	}
	l.refs++
	r.mu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		r.mu.Lock()
		if l.refs--; l.refs == 0 {
			delete(r.held, key)
		}
		r.mu.Unlock()
	}
}

func newRequest(id string, params ...string) orch.Request {
	b, _ := json.Marshal(params)
	sum := sha256.Sum256(b)
	return orch.Request{ID: id, Hash: hex.EncodeToString(sum[:])}
}

func errRequestReused(id string) error {
	return fmt.Errorf("request_id %q was already used with different parameters", id)
}

// sentRequest returns the message an earlier send with req created.
func (d *Node) sentRequest(ctx context.Context, from api.Actor, req orch.Request) (api.OrchSendResult, bool, error) {
	m, hash, ok, err := d.orch.MessageByRequest(ctx, from, req.ID)
	if err != nil || !ok {
		return api.OrchSendResult{}, false, err
	}
	if hash != req.Hash {
		return api.OrchSendResult{}, false, errRequestReused(req.ID)
	}
	return api.OrchSendResult{MessageID: m.ID}, true, nil
}

// spawnedRequest returns the result of an earlier spawn with req.
func (d *Node) spawnedRequest(ctx context.Context, parent api.Actor, req orch.Request) (api.OrchSpawnResult, bool, error) {
	prev, ok, err := d.orch.LinkByRequest(ctx, parent, req.ID)
	if err != nil || !ok {
		return api.OrchSpawnResult{}, false, err
	}
	if prev.Hash != req.Hash {
		return api.OrchSpawnResult{}, false, errRequestReused(req.ID)
	}
	return api.OrchSpawnResult{SessionID: prev.Link.ChildSessionID, TaskID: prev.Link.TaskID}, true, nil
}
