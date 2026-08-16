package node

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

func loadCurrentLog(st *trustlog.SyncStore) ([]trustlog.Entry, *trustlog.Log, error) {
	chain := st.Bytes()
	if chain == nil {
		return nil, nil, &api.RPCError{Code: api.CodeInternalError, Message: "no chain in trust store"}
	}
	entries, err := trustlog.UnmarshalChain(chain)
	if err != nil {
		return nil, nil, &api.RPCError{Code: api.CodeInternalError, Message: "unmarshal chain: " + err.Error()}
	}
	log, err := trustlog.Load(entries)
	if err != nil {
		return nil, nil, &api.RPCError{Code: api.CodeInternalError, Message: "load log: " + err.Error()}
	}
	return entries, log, nil
}

func kindString(k trustlog.Kind) string {
	switch k {
	case trustlog.KindGenesis:
		return "genesis"
	case trustlog.KindAddSigner:
		return "add-signer"
	case trustlog.KindRemoveSigner:
		return "remove-signer"
	case trustlog.KindAuthorizeDevice:
		return "authorize-device"
	case trustlog.KindRevokeDevice:
		return "revoke-device"
	case trustlog.KindRevokeSigner:
		return "revoke-signer"
	case trustlog.KindDisable:
		return "disable"
	default:
		return fmt.Sprintf("kind(%d)", k)
	}
}

// readPinState fills the pin/quarantine fields of a status result under pinMu.
func (d *Node) readPinState(res *api.LockStatusResult) {
	d.pinMu.Lock()
	defer d.pinMu.Unlock()
	res.Quarantined = d.trustGate.Tripped()
	res.Pinned = len(d.pinGenesis) > 0
	if res.Pinned {
		res.PinGenesis = append([]byte(nil), d.pinGenesis...)
		res.PinSource = d.pinSource
	}
	if res.Quarantined {
		res.SeenGenesis = d.trustGate.Genesis()
	}
}

func (d *Node) handleLockPin(_ context.Context, raw json.RawMessage) (any, error) {
	var p api.LockPinParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
		}
	}
	if err := d.AdoptPin(p.Genesis); err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: err.Error()}
	}
	return nil, nil
}

func (d *Node) handleLockUnpin(_ context.Context, _ json.RawMessage) (any, error) {
	if err := d.DropPin(); err != nil {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: err.Error()}
	}
	return nil, nil
}

func (d *Node) handleLockLocalDisable(_ context.Context, _ json.RawMessage) (any, error) {
	if err := d.LocalDisable(); err != nil {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: err.Error()}
	}
	return nil, nil
}

func (d *Node) handleLockStatus(_ context.Context, _ json.RawMessage) (any, error) {
	signerPub := d.SignerPublic()
	res := api.LockStatusResult{
		SignerPubKey:   signerPub,
		IdentityPubKey: append([]byte(nil), d.identity.Public...),
		LocalDisabled:  d.localDisabled(),
	}
	d.readPinState(&res)
	st := d.trust.Load()
	if st == nil {
		return res, nil
	}
	res.Enabled = true
	res.Tip = st.Tip()
	res.Signers = st.Signers()
	devices := st.Devices()
	res.DeviceCount = len(devices)
	res.Devices = devices
	res.Length = st.Length()
	if len(signerPub) > 0 {
		res.SignerTrusted = st.SignerTrusted(signerPub)
	}
	if len(d.identity.Public) > 0 {
		res.Authorized = st.DeviceAuthorized(d.identity.Public)
	}
	res.Disabled = st.Disabled()
	return res, nil
}

func (d *Node) handleLockLog(_ context.Context, _ json.RawMessage) (any, error) {
	st := d.trust.Load()
	if st == nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "locked mode not enabled"}
	}
	entries, log, err := loadCurrentLog(st)
	if err != nil {
		return nil, err
	}
	out := make([]api.LockLogEntry, len(entries))
	for i, e := range entries {
		le := api.LockLogEntry{Index: i, Kind: kindString(e.Kind), Hash: trustlog.HashEntry(&entries[i])}
		switch e.Kind {
		case trustlog.KindGenesis:
			le.Signers = make([][]byte, len(e.Signers))
			for j, s := range e.Signers {
				le.Signers[j] = append([]byte(nil), s...)
			}
		case trustlog.KindAddSigner, trustlog.KindRemoveSigner,
			trustlog.KindAuthorizeDevice, trustlog.KindRevokeDevice:
			le.Target = append([]byte(nil), e.Key...)
		case trustlog.KindRevokeSigner:
			le.Revoked = make([][]byte, len(e.Signers))
			for j, s := range e.Signers {
				le.Revoked[j] = append([]byte(nil), s...)
			}
			le.Replaces = make([][]byte, len(e.Replaces))
			for j, s := range e.Replaces {
				le.Replaces[j] = append([]byte(nil), s...)
			}
			le.CoSignCount = len(e.CoSigns)
		}
		out[i] = le
	}
	return api.LockLogResult{
		Entries: out,
		Tip:     st.Tip(),
		Signers: log.Signers(),
	}, nil
}
