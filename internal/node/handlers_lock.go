package node

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/keyfmt"
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

// handleLockInit establishes the trust log (lock.init). Once-only (unless the log
// is disabled). The caller must list this node's own signer key explicitly.
func (d *Node) handleLockInit(_ context.Context, params json.RawMessage) (any, error) {
	if st := d.trust.Load(); st != nil && !st.Disabled() {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "locked mode already enabled"}
	}
	if len(d.signer.Public) != ed25519.PublicKeySize {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "node has no signer key"}
	}
	if d.trustPath == "" {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "trust state path not configured"}
	}
	p, err := api.Decode[api.LockInitParams](params)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
	}
	if p.GenDisablements < 0 {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "gen_disablements must be non-negative"}
	}

	// Deduplicate the caller-supplied signer set. This node's key must appear explicitly.
	signerSet := make([][]byte, 0, len(p.Signers))
	seen := map[string]bool{}
	for _, s := range p.Signers {
		if len(s) != ed25519.PublicKeySize {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "bad signer pubkey length"}
		}
		if !seen[string(s)] {
			seen[string(s)] = true
			signerSet = append(signerSet, append([]byte(nil), s...))
		}
	}
	if !seen[string(d.signer.Public)] {
		return nil, &api.RPCError{
			Code: api.CodeInvalidRequest,
			Message: "this node's own signer key must be listed explicitly: " +
				keyfmt.SignerKey.Encode(d.signer.Public),
		}
	}
	for _, dev := range p.Devices {
		if len(dev) != 32 {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "bad device pubkey length"}
		}
	}

	var secrets, commitments [][]byte
	for i := 0; i < p.GenDisablements; i++ {
		secret, err := trustlog.GenerateDisablementSecret()
		if err != nil {
			return nil, &api.RPCError{Code: api.CodeInternalError, Message: "disablement secret: " + err.Error()}
		}
		secrets = append(secrets, secret)
		commitments = append(commitments, trustlog.DisablementCommitment(secret))
	}

	tlog, err := trustlog.NewGenesis(signerSet, d.signer, commitments)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "genesis: " + err.Error()}
	}
	genesisHash := tlog.Tip()
	seenDev := map[string]bool{}
	for _, dev := range p.Devices {
		if seenDev[string(dev)] {
			continue
		}
		seenDev[string(dev)] = true
		if err := tlog.AuthorizeDevice(dev, d.signer); err != nil {
			return nil, &api.RPCError{Code: api.CodeInternalError, Message: "authorize: " + err.Error()}
		}
	}

	store := trustlog.NewSyncStore(genesisHash)
	if _, err := store.Ingest(trustlog.MarshalChain(tlog.Entries())); err != nil {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "ingest: " + err.Error()}
	}
	if err := d.activateTrust(store, genesisHash, d.trustPath); err != nil {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "activate: " + err.Error()}
	}
	return api.LockInitResult{Tip: genesisHash, SignerCount: len(signerSet), DisablementSecrets: secrets}, nil
}

// handleLockSign authorizes a device (lock.sign). Requires this node to be a trusted signer.
func (d *Node) handleLockSign(_ context.Context, params json.RawMessage) (any, error) {
	return d.lockDevice(params, true)
}

// handleLockRevoke revokes a device (lock.revoke). Idempotent.
func (d *Node) handleLockRevoke(_ context.Context, params json.RawMessage) (any, error) {
	return d.lockDevice(params, false)
}

func (d *Node) lockDevice(params json.RawMessage, authorize bool) (any, error) {
	st := d.trust.Load()
	if st == nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "locked mode not enabled"}
	}
	if !st.SignerTrusted(d.signer.Public) {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "this node is not a trusted signer; run on a signer node"}
	}
	p, err := api.Decode[api.LockDeviceParams](params)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
	}
	if len(p.Device) != 32 {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "device must be a 32-byte identity pubkey"}
	}
	var changed bool
	if authorize {
		changed, err = st.AuthorizeDevice(p.Device, d.signer)
	} else {
		changed, err = st.RevokeDevice(p.Device, d.signer)
	}
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "append: " + err.Error()}
	}
	if changed {
		if werr := d.persistTrust(); werr != nil {
			d.log.Warn("persisting trust-log chain failed", "path", d.trustPath, "err", werr)
		}
		d.reevaluateTrustChannels()
		d.announceTrustChange()
	}
	return api.LockDeviceResult{Tip: st.Tip(), Changed: changed}, nil
}

// handleLockAddSigner adds a trusted signer (lock.addSigner). Idempotent.
func (d *Node) handleLockAddSigner(_ context.Context, params json.RawMessage) (any, error) {
	return d.lockSigner(params, true)
}

// handleLockRemoveSigner removes a trusted signer (lock.removeSigner). Idempotent.
func (d *Node) handleLockRemoveSigner(_ context.Context, params json.RawMessage) (any, error) {
	return d.lockSigner(params, false)
}

func (d *Node) lockSigner(params json.RawMessage, add bool) (any, error) {
	st := d.trust.Load()
	if st == nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "locked mode not enabled"}
	}
	if !st.SignerTrusted(d.signer.Public) {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "this node is not a trusted signer; run on a signer node"}
	}
	p, err := api.Decode[api.LockSignerParams](params)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
	}
	if len(p.Signer) != 32 {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "signer must be a 32-byte ed25519 pubkey"}
	}
	var changed bool
	if add {
		changed, err = st.AddSigner(p.Signer, d.signer)
	} else {
		changed, err = st.RemoveSigner(p.Signer, d.signer)
	}
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "append: " + err.Error()}
	}
	if changed {
		if werr := d.persistTrust(); werr != nil {
			d.log.Warn("persisting trust-log chain failed", "path", d.trustPath, "err", werr)
		}
		d.reevaluateTrustChannels()
		d.announceTrustChange()
	}
	return api.LockDeviceResult{Tip: st.Tip(), Changed: changed}, nil
}

// handleLockDisable consumes a disablement secret (lock.disable). Authorized by
// the secret, not a signer — so it works even when this node is not in the trust set.
func (d *Node) handleLockDisable(_ context.Context, params json.RawMessage) (any, error) {
	st := d.trust.Load()
	if st == nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "locked mode not enabled"}
	}
	if len(d.signer.Private) != ed25519.PrivateKeySize {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "node has no signer key"}
	}
	p, err := api.Decode[api.LockDisableParams](params)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
	}
	changed, derr := st.Disable(p.Secret, d.signer)
	if derr != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "disable: " + derr.Error()}
	}
	if changed {
		if werr := d.persistTrust(); werr != nil {
			d.log.Warn("persisting trust-log chain failed", "path", d.trustPath, "err", werr)
		}
		d.reevaluateTrustChannels()
		d.announceTrustChange()
	}
	return api.LockDisableResult{Tip: st.Tip(), Disabled: st.Disabled()}, nil
}

func (d *Node) handleLockPin(_ context.Context, raw json.RawMessage) (any, error) {
	var p api.LockPinParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
		}
	}
	if err := d.AdoptPin(p.Genesis); err != nil {
		code := api.CodeInternalError
		if errors.Is(err, ErrPinGenesisLen) || errors.Is(err, ErrPinConflict) {
			code = api.CodeInvalidRequest
		}
		return nil, &api.RPCError{Code: code, Message: err.Error()}
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
