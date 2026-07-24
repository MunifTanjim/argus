package trustlog

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
)

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte(nil), b...)
}

func cloneSigners(s [][]byte) [][]byte {
	if s == nil {
		return nil
	}
	out := make([][]byte, len(s))
	for i := range s {
		out[i] = cloneBytes(s[i])
	}
	return out
}

func cloneEntry(e Entry) Entry {
	e.Prev = cloneBytes(e.Prev)
	e.Signers = cloneSigners(e.Signers)
	e.Disablements = cloneSigners(e.Disablements)
	e.Key = cloneBytes(e.Key)
	e.Signer = cloneBytes(e.Signer)
	e.Sig = cloneBytes(e.Sig)
	if e.CoSigns != nil {
		cs := make([]CoSign, len(e.CoSigns))
		for i, c := range e.CoSigns {
			cs[i] = CoSign{Signer: cloneBytes(c.Signer), Sig: cloneBytes(c.Sig)}
		}
		e.CoSigns = cs
	}
	e.Replaces = cloneSigners(e.Replaces)
	return e
}

// Log is an append-only trust ledger: a hash-chained sequence of signed entries
// and the trusted-signer / authorized-device state folded from them.
// A Log is not safe for concurrent use.
type Log struct {
	entries      []Entry
	tip          []byte
	signers      map[string]bool   // trusted signer pubkey (string(bytes)) -> true
	devices      map[string]bool   // authorized device pubkey -> true
	deviceSigner map[string][]byte // device pubkey -> signer pubkey that authorized it
	disablements [][]byte          // genesis disablement commitments
	disabled     bool              // set by a valid KindDisable entry (sticky)
}

func newEmpty() *Log {
	return &Log{signers: map[string]bool{}, devices: map[string]bool{}, deviceSigner: map[string][]byte{}}
}

func containsBytes(set [][]byte, b []byte) bool {
	for _, s := range set {
		if bytes.Equal(s, b) {
			return true
		}
	}
	return false
}

// NewGenesis starts a log with an initial trusted signer set, self-signed by `by`
// (which must be one of `signers`). disablements is an optional list of Argon2id
// commitments of disablement secrets; pass nil if not needed.
func NewGenesis(signers [][]byte, by SignerKey, disablements [][]byte) (*Log, error) {
	if len(signers) == 0 {
		return nil, errors.New("trustlog: genesis requires at least one signer")
	}
	if !containsBytes(signers, by.Public) {
		return nil, errors.New("trustlog: genesis signer must be in the signer set")
	}
	e := Entry{Kind: KindGenesis, Signers: cloneSigners(signers), Disablements: cloneSigners(disablements)}
	sign(&e, by)
	l := newEmpty()
	if err := l.apply(&e); err != nil {
		return nil, err
	}
	return l, nil
}

// apply verifies an entry against current state and folds it in.
func (l *Log) apply(e *Entry) error {
	if err := l.verify(e); err != nil {
		return err
	}
	l.fold(e)
	return nil
}

// verify runs every read-only check for e against the current state. It MUST NOT
// mutate l. Splitting verification from folding lets fork-choice re-fold an
// already-verified prefix (fold only) without paying re-verification.
func (l *Log) verify(e *Entry) error {
	// KindRevokeSigner is authenticated by co-signs, not a single Signer+Sig pair.
	if e.Kind != KindRevokeSigner && !verifySig(e) {
		return errors.New("trustlog: bad signature")
	}
	if e.Kind == KindGenesis {
		if len(l.entries) != 0 {
			return errors.New("trustlog: genesis must be the first entry")
		}
		if len(e.Signers) == 0 {
			return errors.New("trustlog: genesis requires at least one signer")
		}
		if e.Prev != nil {
			return errors.New("trustlog: genesis must have no prev")
		}
		if !containsBytes(e.Signers, e.Signer) {
			return errors.New("trustlog: genesis signer not in its signer set")
		}
		return nil
	}
	if len(l.entries) == 0 {
		return errors.New("trustlog: first entry must be genesis")
	}
	if !bytes.Equal(e.Prev, l.tip) {
		return errors.New("trustlog: entry does not extend the current tip")
	}
	if l.disabled {
		return errors.New("trustlog: log is disabled; no further entries accepted")
	}
	switch e.Kind {
	case KindRevokeSigner:
		if _, ok := validCoSigns(e, func(p []byte) bool { return l.signers[string(p)] }, len(e.Replaces) > 0); !ok {
			return errors.New("trustlog: revoke-signer lacks enough valid co-signs")
		}
		if remainingAfterRevokeWith(l.signers, e.Replaces, e.Signers) < 1 {
			return errors.New("trustlog: revoke-signer would leave zero signers")
		}
		return nil
	case KindDisable:
		if !containsBytes(l.disablements, DisablementCommitment(e.Key)) {
			return errors.New("trustlog: disable secret does not match any genesis commitment")
		}
		return nil
	case KindAddSigner:
		if !l.signers[string(e.Signer)] {
			return errors.New("trustlog: entry not signed by a trusted signer")
		}
		return nil
	case KindRemoveSigner:
		if !l.signers[string(e.Signer)] {
			return errors.New("trustlog: entry not signed by a trusted signer")
		}
		if !l.signers[string(e.Key)] {
			return errors.New("trustlog: cannot remove an unknown signer")
		}
		if len(l.signers) == 1 {
			return errors.New("trustlog: cannot remove the last signer")
		}
		return nil
	case KindAuthorizeDevice:
		if !l.signers[string(e.Signer)] {
			return errors.New("trustlog: entry not signed by a trusted signer")
		}
		if l.devices[string(e.Key)] {
			return errors.New("trustlog: device already authorized")
		}
		return nil
	case KindRevokeDevice:
		if !l.signers[string(e.Signer)] {
			return errors.New("trustlog: entry not signed by a trusted signer")
		}
		return nil
	default:
		if !l.signers[string(e.Signer)] {
			return errors.New("trustlog: entry not signed by a trusted signer")
		}
		return errors.New("trustlog: unknown entry kind")
	}
}

// fold applies e's state transitions. It MUST NOT check anything — callers
// guarantee e was already verified (Load calls verify first; foldSignersAt folds
// an already-verified prefix). It always appends the entry and advances the tip.
func (l *Log) fold(e *Entry) {
	switch e.Kind {
	case KindGenesis:
		for _, s := range e.Signers {
			l.signers[string(s)] = true
		}
		l.disablements = cloneSigners(e.Disablements)
	case KindRevokeSigner:
		for _, r := range e.Replaces {
			l.signers[string(r)] = true
		}
		for _, r := range e.Signers {
			delete(l.signers, string(r))
			l.dropDevicesAuthorizedBy(r)
		}
	case KindDisable:
		l.disabled = true
	case KindAddSigner:
		l.signers[string(e.Key)] = true
	case KindRemoveSigner:
		delete(l.signers, string(e.Key))
		l.dropDevicesAuthorizedBy(e.Key)
	case KindAuthorizeDevice:
		l.devices[string(e.Key)] = true
		l.deviceSigner[string(e.Key)] = cloneBytes(e.Signer)
	case KindRevokeDevice:
		delete(l.devices, string(e.Key))
		delete(l.deviceSigner, string(e.Key))
	}
	l.entries = append(l.entries, *e)
	l.tip = hashEntry(e)
}

// remainingAfterRevokeWith returns how many signers remain after adding replaces
// to the set then removing the distinct revoked pubkeys — computed WITHOUT
// mutating signers (the read-only form used by verify, equivalent to adding
// replaces first then calling remainingAfterRevoke).
func remainingAfterRevokeWith(signers map[string]bool, replaces, revoked [][]byte) int {
	withReplaces := len(signers)
	seenReplaces := map[string]bool{}
	for _, r := range replaces {
		rs := string(r)
		if !signers[rs] && !seenReplaces[rs] {
			seenReplaces[rs] = true
			withReplaces++
		}
	}
	seen := map[string]bool{}
	present := func(k string) bool {
		if signers[k] {
			return true
		}
		return containsBytes(replaces, []byte(k))
	}
	remaining := withReplaces
	for _, r := range revoked {
		rs := string(r)
		if present(rs) && !seen[rs] {
			seen[rs] = true
			remaining--
		}
	}
	return remaining
}

// remainingAfterRevoke returns how many signers remain in the set after removing
// the distinct revoked pubkeys (callers add any replacements to signers first).
func remainingAfterRevoke(signers map[string]bool, revoked [][]byte) int {
	remaining := len(signers)
	seen := map[string]bool{}
	for _, r := range revoked {
		rs := string(r)
		if signers[rs] && !seen[rs] {
			seen[rs] = true
			remaining--
		}
	}
	return remaining
}

// dropDevicesAuthorizedBy removes every device whose authorizing signer is pub —
// retroactive invalidation when that signer is removed or revoked.
func (l *Log) dropDevicesAuthorizedBy(pub []byte) {
	for dev, signer := range l.deviceSigner {
		if bytes.Equal(signer, pub) {
			delete(l.devices, dev)
			delete(l.deviceSigner, dev)
		}
	}
}

// appendEntry builds, signs (by `by`), and applies a non-genesis entry.
func (l *Log) appendEntry(kind Kind, key []byte, by SignerKey) error {
	e := Entry{Kind: kind, Prev: cloneBytes(l.tip), Key: cloneBytes(key)}
	sign(&e, by)
	return l.apply(&e)
}

// Disable appends a KindDisable entry revealing `secret`; it is accepted only if
// secret's commitment is in the genesis. `by` may be any keypair (its signature only
// binds the entry into the chain — a disablement secret, not signer trust, authorizes).
func (l *Log) Disable(secret []byte, by SignerKey) error {
	return l.appendEntry(KindDisable, secret, by)
}

// Disabled reports whether the log has been disabled by a valid disablement secret.
func (l *Log) Disabled() bool { return l.disabled }

// AuthorizeDevice records a device pubkey as authorized (signed by a trusted signer).
func (l *Log) AuthorizeDevice(devicePub []byte, by SignerKey) error {
	return l.appendEntry(KindAuthorizeDevice, devicePub, by)
}

// RevokeDevice revokes a previously-authorized device.
func (l *Log) RevokeDevice(devicePub []byte, by SignerKey) error {
	return l.appendEntry(KindRevokeDevice, devicePub, by)
}

// DeviceAuthorized reports whether a device pubkey is currently authorized.
func (l *Log) DeviceAuthorized(pub []byte) bool { return l.devices[string(pub)] }

// SignerTrusted reports whether a signer pubkey is currently trusted.
func (l *Log) SignerTrusted(pub []byte) bool { return l.signers[string(pub)] }

// Tip returns the current chain tip (BLAKE2s of the latest entry) — the audit
// fingerprint compared out-of-band across nodes.
func (l *Log) Tip() []byte { return append([]byte(nil), l.tip...) }

// Entries returns a copy of the chain.
func (l *Log) Entries() []Entry {
	out := make([]Entry, len(l.entries))
	for i := range l.entries {
		out[i] = cloneEntry(l.entries[i])
	}
	return out
}

// Load folds and verifies a chain from genesis, reproducing its state. It rejects
// any chain whose entries are tampered, reordered, rolled back onto a bad link,
// or edited by an untrusted signer. The caller must independently trust the
// genesis (e.g. by pinning Tip() out-of-band) — Load proves the rest follows.
func Load(entries []Entry) (*Log, error) {
	l := newEmpty()
	for i := range entries {
		if err := l.apply(&entries[i]); err != nil {
			return nil, fmt.Errorf("trustlog: entry %d: %w", i, err)
		}
	}
	if len(l.entries) == 0 {
		return nil, errors.New("trustlog: empty chain")
	}
	return l, nil
}

// RevokeSigner revokes one or more signers by building and applying a
// KindRevokeSigner entry co-signed by `by`. replaces optionally lists replacement
// signer pubkeys added atomically. The co-sign count must exceed the number of
// revoked signers; post-apply signer count must be ≥1.
func (l *Log) RevokeSigner(revoked [][]byte, replaces [][]byte, by []SignerKey) error {
	e := newRevokeSignerEntry(cloneBytes(l.tip), revoked, replaces, by)
	return l.apply(&e)
}

// AddSigner records a new trusted signer (signed by an existing trusted signer).
func (l *Log) AddSigner(signerPub []byte, by SignerKey) error {
	return l.appendEntry(KindAddSigner, signerPub, by)
}

// RemoveSigner removes a trusted signer (signed by an existing trusted signer);
// the last signer cannot be removed.
func (l *Log) RemoveSigner(signerPub []byte, by SignerKey) error {
	return l.appendEntry(KindRemoveSigner, signerPub, by)
}

// sortedKeys returns the map keys as sorted, freshly-copied byte slices.
func sortedKeys(m map[string]bool) [][]byte {
	out := make([][]byte, 0, len(m))
	for k := range m {
		out = append(out, []byte(k))
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i], out[j]) < 0 })
	return out
}

// Signers returns the currently-trusted signer pubkeys (sorted copies).
func (l *Log) Signers() [][]byte { return sortedKeys(l.signers) }

// Devices returns the currently-authorized device pubkeys (sorted copies).
func (l *Log) Devices() [][]byte { return sortedKeys(l.devices) }

// clone returns a deep copy of the log: callers may mutate the copy (e.g. append
// a new entry) without affecting the original adopted log.
func (l *Log) clone() *Log {
	c := &Log{
		entries:      make([]Entry, len(l.entries)),
		tip:          append([]byte(nil), l.tip...),
		signers:      make(map[string]bool, len(l.signers)),
		devices:      make(map[string]bool, len(l.devices)),
		deviceSigner: make(map[string][]byte, len(l.deviceSigner)),
		disablements: cloneSigners(l.disablements),
		disabled:     l.disabled,
	}
	for i := range l.entries {
		c.entries[i] = cloneEntry(l.entries[i])
	}
	for k, v := range l.signers {
		c.signers[k] = v
	}
	for k, v := range l.devices {
		c.devices[k] = v
	}
	for k, v := range l.deviceSigner {
		c.deviceSigner[k] = append([]byte(nil), v...)
	}
	return c
}
