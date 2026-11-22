package node

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/invite"
)

func invalidRequest(msg string) *api.RPCError {
	return &api.RPCError{Code: api.CodeInvalidRequest, Message: msg}
}

func (d *Node) inviteStore() (*invite.Store, *api.RPCError) {
	if d.invites == nil {
		return nil, invalidRequest(api.ErrMsgInvitesOff)
	}
	return d.invites, nil
}

func (d *Node) handleLockInviteCreate(_ context.Context, params json.RawMessage) (any, error) {
	store, rerr := d.inviteStore()
	if rerr != nil {
		return nil, rerr
	}
	if _, rerr := d.requireSigner(); rerr != nil {
		return nil, rerr
	}
	p, err := api.Decode[api.LockInviteCreateParams](params)
	if err != nil {
		return nil, invalidRequest("invalid params: " + err.Error())
	}
	d.pinMu.Lock()
	genesis := append([]byte(nil), d.pinGenesis...)
	d.pinMu.Unlock()
	if len(genesis) == 0 {
		return nil, invalidRequest("this node is not pinned; run argus lock pin")
	}
	// Bound the seconds before converting, so a huge value cannot overflow
	// into a valid duration.
	if p.TTLSeconds <= 0 || p.TTLSeconds > int64(invite.MaxTTL/time.Second) {
		return nil, invalidRequest(fmt.Sprintf("ttl_seconds must be from 1 to %d", int64(invite.MaxTTL/time.Second)))
	}
	inv, err := store.Create(time.Duration(p.TTLSeconds) * time.Second)
	if err != nil {
		return nil, invalidRequest(err.Error())
	}
	return api.LockInviteCreateResult{
		InviteID:       inv.ID,
		Secret:         inv.Secret,
		ExpiresAt:      inv.ExpiresAt,
		NodeID:         d.id,
		IdentityPubKey: d.identity.Public,
		Genesis:        genesis,
	}, nil
}

func (d *Node) handleLockInviteList(_ context.Context, _ json.RawMessage) (any, error) {
	store, rerr := d.inviteStore()
	if rerr != nil {
		return nil, rerr
	}
	invs, err := store.List()
	if err != nil {
		return nil, invalidRequest(err.Error())
	}
	now := store.Now()
	res := api.LockInviteListResult{Invites: make([]api.LockInviteInfo, 0, len(invs))}
	for _, inv := range invs {
		info := api.LockInviteInfo{
			ID:        inv.ID,
			CreatedAt: inv.CreatedAt,
			ExpiresAt: inv.ExpiresAt,
			State:     string(inv.State),
			Keys:      inv.Keys,
		}
		if inv.Expired(now) {
			info.State = "expired"
		}
		if !inv.UsedAt.IsZero() {
			info.UsedAt = &inv.UsedAt
		}
		res.Invites = append(res.Invites, info)
	}
	return res, nil
}

func (d *Node) handleLockInviteRevoke(_ context.Context, params json.RawMessage) (any, error) {
	store, rerr := d.inviteStore()
	if rerr != nil {
		return nil, rerr
	}
	p, err := api.Decode[api.LockInviteRevokeParams](params)
	if err != nil {
		return nil, invalidRequest("invalid params: " + err.Error())
	}
	if err := store.Revoke(p.ID); err != nil {
		return nil, invalidRequest(err.Error())
	}
	return nil, nil
}
