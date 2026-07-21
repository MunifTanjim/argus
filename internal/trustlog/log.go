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
	e.Key = cloneBytes(e.Key)
	e.Signer = cloneBytes(e.Signer)
	e.Sig = cloneBytes(e.Sig)
	return e
}

// Log is an append-only trust ledger: a hash-chained sequence of signed entries
// and the trusted-signer / authorized-device state folded from them.
// A Log is not safe for concurrent use.
type Log struct {
	entries []Entry
	head    []byte
	signers map[string]bool // trusted signer pubkey (string(bytes)) -> true
	devices map[string]bool // authorized device pubkey -> true
}

func newEmpty() *Log {
	return &Log{signers: map[string]bool{}, devices: map[string]bool{}}
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
// (which must be one of `signers`).
func NewGenesis(signers [][]byte, by SignerKey) (*Log, error) {
	if len(signers) == 0 {
		return nil, errors.New("trustlog: genesis requires at least one signer")
	}
	if !containsBytes(signers, by.Public) {
		return nil, errors.New("trustlog: genesis signer must be in the signer set")
	}
	e := Entry{Kind: KindGenesis, Signers: cloneSigners(signers)}
	sign(&e, by)
	l := newEmpty()
	if err := l.apply(&e); err != nil {
		return nil, err
	}
	return l, nil
}

// apply verifies an entry against current state and folds it in.
func (l *Log) apply(e *Entry) error {
	if !verifySig(e) {
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
		for _, s := range e.Signers {
			l.signers[string(s)] = true
		}
	} else {
		if len(l.entries) == 0 {
			return errors.New("trustlog: first entry must be genesis")
		}
		if !bytes.Equal(e.Prev, l.head) {
			return errors.New("trustlog: entry does not extend the current head")
		}
		if !l.signers[string(e.Signer)] {
			return errors.New("trustlog: entry not signed by a trusted signer")
		}
		switch e.Kind {
		case KindAddSigner:
			l.signers[string(e.Key)] = true
		case KindRemoveSigner:
			if !l.signers[string(e.Key)] {
				return errors.New("trustlog: cannot remove an unknown signer")
			}
			if len(l.signers) == 1 {
				return errors.New("trustlog: cannot remove the last signer")
			}
			delete(l.signers, string(e.Key))
		case KindAuthorizeDevice:
			l.devices[string(e.Key)] = true
		case KindRevokeDevice:
			delete(l.devices, string(e.Key))
		default:
			return errors.New("trustlog: unknown entry kind")
		}
	}
	l.entries = append(l.entries, *e)
	l.head = hashEntry(e)
	return nil
}

// appendEntry builds, signs (by `by`), and applies a non-genesis entry.
func (l *Log) appendEntry(kind Kind, key []byte, by SignerKey) error {
	e := Entry{Kind: kind, Prev: cloneBytes(l.head), Key: cloneBytes(key)}
	sign(&e, by)
	return l.apply(&e)
}

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

// Head returns the current chain head (BLAKE2s of the latest entry) — the audit
// fingerprint compared out-of-band across nodes.
func (l *Log) Head() []byte { return append([]byte(nil), l.head...) }

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
// genesis (e.g. by pinning Head() out-of-band) — Load proves the rest follows.
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
