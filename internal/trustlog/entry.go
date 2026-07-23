// Package trustlog is a hash-chained, signer-signed, append-only trust ledger for
// argus locked mode (the equivalent of Tailscale's Tailnet Key Authority). It
// tracks the trusted signer set and the authorized device set, and rejects
// tampering, reordering, rollback, and edits not signed by a trusted signer.
package trustlog

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"

	"golang.org/x/crypto/blake2s"
)

// SignerKey is an Ed25519 keypair authorized to sign trust-log entries.
type SignerKey struct {
	Public  ed25519.PublicKey
	Private ed25519.PrivateKey
}

// GenerateSigner returns a fresh Ed25519 signer keypair.
func GenerateSigner() (SignerKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return SignerKey{}, fmt.Errorf("trustlog: generate signer: %w", err)
	}
	return SignerKey{Public: pub, Private: priv}, nil
}

// Kind is the type of a trust-log entry.
type Kind uint8

const (
	KindGenesis         Kind = iota + 1 // establishes the initial trusted signer set
	KindAddSigner                       // adds a trusted signer (Key = signer pubkey)
	KindRemoveSigner                    // removes a trusted signer (Key = signer pubkey)
	KindAuthorizeDevice                 // authorizes a device (Key = device pubkey)
	KindRevokeDevice                    // revokes a device (Key = device pubkey)
	KindDisable                         // disables the log (Key = revealed disablement secret; authorized by commitment, not a signer)
	KindRevokeSigner                    // revokes one or more signers (Signers = revoked pubkeys; Replaces = replacement pubkeys; requires co-signs)
)

// CoSign is one signer's signature over a co-signed entry's sigBytes.
type CoSign struct {
	Signer []byte
	Sig    []byte
}

// Entry is one link in the trust log. Genesis carries the initial signer set in
// Signers and is self-anchored (Signer is one of Signers). Every other entry has
// Prev = the previous entry's hash and Signer = a currently-trusted signer.
type Entry struct {
	Kind         Kind
	Prev         []byte   // hash of the previous entry; nil for genesis
	Signers      [][]byte // genesis only: initial trusted signer pubkeys; KindRevokeSigner: revoked signer pubkeys
	Disablements [][]byte // genesis only: Argon2id commitments of disablement secrets
	Key          []byte   // add/remove-signer or authorize/revoke-device target pubkey
	Signer       []byte   // pubkey of the signer that signed this entry
	Sig          []byte   // Ed25519 signature over sigBytes(entry)
	CoSigns      []CoSign // KindRevokeSigner only: co-signatures from remaining trusted signers
	Replaces     [][]byte // KindRevokeSigner only: replacement signer pubkeys added atomically
}

func putField(buf *bytes.Buffer, b []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(b)))
	buf.Write(n[:])
	buf.Write(b)
}

// sigBytes is the deterministic encoding an entry's signature covers: every field
// EXCEPT Sig and CoSigns. Length-prefixed fields in a fixed order. For
// KindRevokeSigner the Replaces count+fields are appended so co-signs attest the
// full replacement set.
func sigBytes(e *Entry) []byte {
	var buf bytes.Buffer
	buf.WriteByte(byte(e.Kind))
	putField(&buf, e.Prev)
	var cnt [4]byte
	binary.BigEndian.PutUint32(cnt[:], uint32(len(e.Signers)))
	buf.Write(cnt[:])
	for _, s := range e.Signers {
		putField(&buf, s)
	}
	var dcnt [4]byte
	binary.BigEndian.PutUint32(dcnt[:], uint32(len(e.Disablements)))
	buf.Write(dcnt[:])
	for _, d := range e.Disablements {
		putField(&buf, d)
	}
	putField(&buf, e.Key)
	putField(&buf, e.Signer)
	if e.Kind == KindRevokeSigner {
		var rcnt [4]byte
		binary.BigEndian.PutUint32(rcnt[:], uint32(len(e.Replaces)))
		buf.Write(rcnt[:])
		for _, r := range e.Replaces {
			putField(&buf, r)
		}
	}
	return buf.Bytes()
}

// hashEntry is the chain hash of a full (signed) entry — it covers Sig, so Prev
// commits to the exact signed predecessor. For KindRevokeSigner the CoSigns are
// also included so the chain commits to the full co-signed payload. Replaces is
// already covered through sigBytes.
func hashEntry(e *Entry) []byte {
	var buf bytes.Buffer
	buf.Write(sigBytes(e))
	putField(&buf, e.Sig)
	if e.Kind == KindRevokeSigner {
		var cnt [4]byte
		binary.BigEndian.PutUint32(cnt[:], uint32(len(e.CoSigns)))
		buf.Write(cnt[:])
		for _, cs := range e.CoSigns {
			putField(&buf, cs.Signer)
			putField(&buf, cs.Sig)
		}
	}
	sum := blake2s.Sum256(buf.Bytes())
	return sum[:]
}

// HashEntry is the exported wrapper for hashEntry — used by cross-package tests
// (e.g. the parity vector generator) that need to compute a chain-link hash.
func HashEntry(e *Entry) []byte { return hashEntry(e) }

// sign sets Signer + Sig on e using key.
func sign(e *Entry, key SignerKey) {
	e.Signer = key.Public
	e.Sig = ed25519.Sign(key.Private, sigBytes(e))
}

// verifySig checks e.Sig against e.Signer over sigBytes(e).
func verifySig(e *Entry) bool {
	if len(e.Signer) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(e.Signer), sigBytes(e), e.Sig)
}

// newRevokeSignerEntry builds a KindRevokeSigner entry: revoked signer pubkeys go
// in Signers, optional replacement pubkeys go in Replaces (both covered by
// sigBytes); each `by` signer co-signs sigBytes.
func newRevokeSignerEntry(prev []byte, revoked [][]byte, replaces [][]byte, by []SignerKey) Entry {
	e := Entry{Kind: KindRevokeSigner, Prev: cloneBytes(prev), Signers: cloneSigners(revoked), Replaces: cloneSigners(replaces)}
	sb := sigBytes(&e)
	for _, k := range by {
		e.CoSigns = append(e.CoSigns, CoSign{Signer: append([]byte(nil), k.Public...), Sig: ed25519.Sign(k.Private, sb)})
	}
	return e
}

// validCoSigns returns the number of DISTINCT valid co-signs from signers trusted
// by `trusted`, and whether that count exceeds the number of revoked signers. By
// default the revoked signers (e.Signers) are excluded from the count; when
// allowRevoked is true they may co-sign — used for KindRevokeSigner with replacement
// signers, where a revoked signer voluntarily authorizes their own succession.
func validCoSigns(e *Entry, trusted func(pub []byte) bool, allowRevoked bool) (int, bool) {
	sb := sigBytes(e)
	revoked := map[string]bool{}
	if !allowRevoked {
		for _, r := range e.Signers {
			revoked[string(r)] = true
		}
	}
	seen := map[string]bool{}
	n := 0
	for _, cs := range e.CoSigns {
		ks := string(cs.Signer)
		if seen[ks] || revoked[ks] || !trusted(cs.Signer) {
			continue
		}
		if len(cs.Signer) != ed25519.PublicKeySize || !ed25519.Verify(ed25519.PublicKey(cs.Signer), sb, cs.Sig) {
			continue
		}
		seen[ks] = true
		n++
	}
	return n, n > len(e.Signers)
}
