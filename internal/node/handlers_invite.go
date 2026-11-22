package node

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/invite"
	"github.com/MunifTanjim/argus/internal/keyfmt"
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

func (d *Node) inviteDispatch(id, clientStatic []byte) api.DispatchFunc {
	return func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method != api.MethodLockInviteRedeem {
			return nil, &api.RPCError{Code: api.CodeMethodNotFound, Message: "method not found: " + method}
		}
		p, err := api.Decode[api.LockInviteRedeemParams](params)
		if err != nil {
			return nil, invalidRequest("invalid params: " + err.Error())
		}
		res, rerr := d.redeemInvite(id, clientStatic, p)
		if rerr != nil {
			return nil, rerr
		}
		return res, nil
	}
}

// redeemInvite consumes invite id and authorizes clientStatic plus the extra key.
// The invite is consumed and persisted before any trust-log append, so a crash
// can lose a redemption but never allow a second one.
func (d *Node) redeemInvite(id, clientStatic []byte, p api.LockInviteRedeemParams) (api.LockInviteRedeemResult, *api.RPCError) {
	if len(p.ExtraKeys) > 1 {
		return api.LockInviteRedeemResult{}, invalidRequest("at most one extra key")
	}
	for _, k := range p.ExtraKeys {
		if len(k) != keyfmt.Len {
			return api.LockInviteRedeemResult{}, invalidRequest("extra key must be a 32-byte identity pubkey")
		}
	}
	st, rerr := d.requireSigner()
	if rerr != nil {
		return api.LockInviteRedeemResult{}, rerr
	}
	if err := d.invites.Consume(id); err != nil {
		if invite.IsRejection(err) {
			return api.LockInviteRedeemResult{}, invalidRequest(err.Error())
		}
		return api.LockInviteRedeemResult{}, &api.RPCError{Code: api.CodeInternalError, Message: "consume invite: " + err.Error()}
	}
	keys := append([][]byte{clientStatic}, p.ExtraKeys...)
	var changed bool
	for _, k := range keys {
		c, err := st.AuthorizeDevice(k, d.signer)
		changed = changed || c
		if err != nil {
			if changed {
				d.afterTrustChange()
			}
			return api.LockInviteRedeemResult{}, &api.RPCError{Code: api.CodeInternalError, Message: "append: " + err.Error()}
		}
	}
	if err := d.invites.RecordKeys(id, keys); err != nil {
		d.log.Warn("recording invite keys failed", "err", err)
	}
	if changed {
		d.afterTrustChange()
	}
	fps := make([]string, len(keys))
	for i, k := range keys {
		fps[i] = base64.StdEncoding.EncodeToString(k[:8])
	}
	d.log.Info("redeemed invite", "invite", hex.EncodeToString(id), "authorized", fps)
	return api.LockInviteRedeemResult{Tip: st.Tip()}, nil
}
