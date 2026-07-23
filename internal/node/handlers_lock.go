package node

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// loadCurrentLog is a convenience helper: it reads the current chain bytes from st,
// unmarshals them, and loads a *Log for callers that need to reason about the log
// state (e.g. the co-signing ceremony handlers).
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

// handleLockInit establishes the trust log (lock.init): builds a genesis whose signer
// set is this node plus the requested additional signers, authorizes the requested
// devices, persists + activates it live, and returns the new genesis hash. Once-only.
func (d *Node) handleLockInit(_ context.Context, params json.RawMessage) (any, error) {
	if d.trust.Load() != nil {
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

	// Signer set = self + additional (deduped). Validate lengths.
	signerSet := [][]byte{append([]byte(nil), d.signer.Public...)}
	seen := map[string]bool{string(d.signer.Public): true}
	for _, s := range p.Signers {
		if len(s) != ed25519.PublicKeySize {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "bad signer pubkey length"}
		}
		if !seen[string(s)] {
			seen[string(s)] = true
			signerSet = append(signerSet, append([]byte(nil), s...))
		}
	}
	for _, dev := range p.Devices {
		if len(dev) != 32 { // Curve25519 identity pubkey
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "bad device pubkey length"}
		}
	}

	// Generate disablement secrets: keep only the commitments (in the genesis); return
	// the raw secrets to the caller ONCE (over the local socket) for the operator to save.
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
	// Capture the genesis hash before appending device entries; NewSyncStore pins
	// this for rollback/fork resistance (Ingest checks entries[0] hash against it).
	genesisHash := tlog.Tip()
	seenDev := map[string]bool{}
	for _, dev := range p.Devices {
		if seenDev[string(dev)] {
			continue // skip duplicates — double-authorize is now rejected at the log level
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

// handleLockSign authorizes a device (lock.sign). Requires this node to be a trusted
// signer in the current chain. Idempotent: a no-op success if already authorized.
func (d *Node) handleLockSign(_ context.Context, params json.RawMessage) (any, error) {
	return d.lockDevice(params, true)
}

// handleLockRevoke revokes a device (lock.revoke). Idempotent: a no-op success if the
// device is not currently authorized.
func (d *Node) handleLockRevoke(_ context.Context, params json.RawMessage) (any, error) {
	return d.lockDevice(params, false)
}

// lockDevice is the shared body of lock.sign/lock.revoke. authorize selects the op.
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
	}
	return api.LockDeviceResult{Tip: st.Tip()}, nil
}

// handleLockAddSigner adds a trusted signer (lock.addSigner). Requires this node to be
// a trusted signer. Idempotent.
func (d *Node) handleLockAddSigner(_ context.Context, params json.RawMessage) (any, error) {
	return d.lockSigner(params, true)
}

// handleLockRemoveSigner removes a trusted signer (lock.removeSigner). Idempotent.
func (d *Node) handleLockRemoveSigner(_ context.Context, params json.RawMessage) (any, error) {
	return d.lockSigner(params, false)
}

// lockSigner is the shared body of lock.addSigner/removeSigner. add selects the op.
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
	}
	return api.LockDeviceResult{Tip: st.Tip()}, nil
}

// handleLockDisable consumes a disablement secret (lock.disable): if its commitment is
// in the genesis, it appends a KindDisable entry — authorized by the secret, not a
// signer — flipping the log (and, via distribution, the whole network) to disabled.
func (d *Node) handleLockDisable(_ context.Context, params json.RawMessage) (any, error) {
	st := d.trust.Load()
	if st == nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "locked mode not enabled"}
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
	}
	return api.LockDisableResult{Tip: st.Tip(), Disabled: st.Disabled()}, nil
}

// handleLockLocalDisable locally disables locked-mode enforcement on this node only.
func (d *Node) handleLockLocalDisable(_ context.Context, _ json.RawMessage) (any, error) {
	if err := d.LocalDisable(); err != nil {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: err.Error()}
	}
	return nil, nil
}

// handleLockRevokeSignerStart begins a revoke-signer co-signing ceremony
// (lock.revokeSignerStart). Requires this node to be a trusted signer. Selects a
// fork point, builds a partial KindRevokeSigner entry, adds this node's co-sign,
// and returns the serialized PendingRevoke blob for the caller to pass to other
// signer nodes for additional co-signs.
func (d *Node) handleLockRevokeSignerStart(_ context.Context, params json.RawMessage) (any, error) {
	st := d.trust.Load()
	if st == nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "locked mode not enabled"}
	}
	if !st.SignerTrusted(d.signer.Public) {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "this node is not a trusted signer; run on a signer node"}
	}
	p, err := api.Decode[api.LockRevokeSignerStartParams](params)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
	}
	if len(p.Revoked) == 0 {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "revoked must be non-empty"}
	}
	_, log, err := loadCurrentLog(st)
	if err != nil {
		return nil, err
	}
	pr, err := trustlog.StartRevoke(log, p.Revoked, p.Replaces, p.ForkFrom, d.signer)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "start revoke: " + err.Error()}
	}
	return api.LockRevokeSignerBlobResult{Blob: pr.Marshal()}, nil
}

// handleLockRevokeSignerCosign adds this node's co-sign to a PendingRevoke blob
// (lock.revokeSignerCosign). Requires this node to be a trusted signer and trusted
// at the blob's fork point. Returns the updated blob.
func (d *Node) handleLockRevokeSignerCosign(_ context.Context, params json.RawMessage) (any, error) {
	st := d.trust.Load()
	if st == nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "locked mode not enabled"}
	}
	if !st.SignerTrusted(d.signer.Public) {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "this node is not a trusted signer; run on a signer node"}
	}
	p, err := api.Decode[api.LockRevokeSignerCosignParams](params)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
	}
	pr, err := trustlog.UnmarshalPendingRevoke(p.Blob)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid blob: " + err.Error()}
	}
	_, log, err := loadCurrentLog(st)
	if err != nil {
		return nil, err
	}
	pr, err = trustlog.AddCoSign(pr, log, d.signer)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "cosign: " + err.Error()}
	}
	return api.LockRevokeSignerBlobResult{Blob: pr.Marshal()}, nil
}

// handleLockRevokeSignerFinish finalizes a completed revoke-signer ceremony
// (lock.revokeSignerFinish). It requires the co-sign quorum to be satisfied
// (Complete), then ingests the resulting KindRevokeSigner entry into the trust store
// via a fork chain, persists the updated chain, and notifies dependent channels.
// Returns the new trust-log tip.
func (d *Node) handleLockRevokeSignerFinish(_ context.Context, params json.RawMessage) (any, error) {
	st := d.trust.Load()
	if st == nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "locked mode not enabled"}
	}
	if !st.SignerTrusted(d.signer.Public) {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "this node is not a trusted signer; run on a signer node"}
	}
	p, err := api.Decode[api.LockRevokeSignerFinishParams](params)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid params: " + err.Error()}
	}
	pr, err := trustlog.UnmarshalPendingRevoke(p.Blob)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "invalid blob: " + err.Error()}
	}
	entries, log, err := loadCurrentLog(st)
	if err != nil {
		return nil, err
	}
	if !trustlog.Complete(pr, log) {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "co-sign quorum not yet reached; collect more co-signs"}
	}
	newChain, err := trustlog.BuildRevokeChain(pr, entries)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "build revoke chain: " + err.Error()}
	}
	changed, err := st.Ingest(newChain)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "ingest: " + err.Error()}
	}
	if changed {
		if werr := d.persistTrust(); werr != nil {
			d.log.Warn("persisting trust-log chain failed", "path", d.trustPath, "err", werr)
		}
		d.reevaluateTrustChannels()
	}
	return api.LockRevokeSignerFinishResult{Tip: st.Tip()}, nil
}

// kindString maps a trustlog.Kind to its wire string (matches the Kind constants in the
// API and the CLI output: "genesis", "add-signer", etc.).
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

// handleLockLog returns the trust-log chain history (lock.log). Read-only; requires
// locked mode to be enabled. The result carries per-entry summaries (kind, target,
// co-sign count) and the current trusted signer set for fingerprint display.
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
		le := api.LockLogEntry{Index: i, Kind: kindString(e.Kind)}
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
		case trustlog.KindDisable:
			// Key is the revealed disablement secret — not exposed in the log API.
		}
		out[i] = le
	}
	return api.LockLogResult{
		Entries: out,
		Tip:     st.Tip(),
		Signers: log.Signers(),
	}, nil
}

// handleLockStatus returns the audit view of this node's locked state.
func (d *Node) handleLockStatus(_ context.Context, _ json.RawMessage) (any, error) {
	res := api.LockStatusResult{
		SignerPubKey:   append([]byte(nil), d.signer.Public...),
		IdentityPubKey: append([]byte(nil), d.identity.Public...),
		LocalDisabled:  d.localDisabled(),
	}
	st := d.trust.Load()
	if st == nil {
		return res, nil
	}
	res.Enabled = true
	res.Tip = st.Tip()
	res.Signers = st.Signers()
	res.DeviceCount = len(st.Devices())
	if len(d.signer.Public) > 0 {
		res.SignerTrusted = st.SignerTrusted(d.signer.Public)
	}
	if len(d.identity.Public) > 0 {
		res.Authorized = st.DeviceAuthorized(d.identity.Public)
	}
	res.Disabled = st.Disabled()
	return res, nil
}
