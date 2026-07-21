package node

import (
	"context"
	"crypto/ed25519"
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// handleLockInit establishes the trust log (lock.init): builds a genesis whose signer
// set is this node plus the requested additional signers, authorizes the requested
// devices, persists + activates it live, and returns the new head. Once-only.
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
	// Capture the genesis head before appending device entries; NewSyncStore pins
	// this for rollback/fork resistance (Ingest checks entries[0] hash against it).
	genesisHead := tlog.Head()
	for _, dev := range p.Devices {
		if err := tlog.AuthorizeDevice(dev, d.signer); err != nil {
			return nil, &api.RPCError{Code: api.CodeInternalError, Message: "authorize: " + err.Error()}
		}
	}

	store := trustlog.NewSyncStore(genesisHead)
	if _, err := store.Ingest(trustlog.MarshalChain(tlog.Entries())); err != nil {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "ingest: " + err.Error()}
	}
	if err := d.activateTrust(store, genesisHead, d.trustPath); err != nil {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "activate: " + err.Error()}
	}
	return api.LockInitResult{Head: genesisHead, SignerCount: len(signerSet), DisablementSecrets: secrets}, nil
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
	}
	return api.LockDeviceResult{Head: st.Head()}, nil
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
	}
	return api.LockDisableResult{Head: st.Head(), Disabled: st.Disabled()}, nil
}

// handleLockLocalDisable locally disables locked-mode enforcement on this node only.
func (d *Node) handleLockLocalDisable(_ context.Context, _ json.RawMessage) (any, error) {
	if err := d.LocalDisable(); err != nil {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: err.Error()}
	}
	return nil, nil
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
	res.Head = st.Head()
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
