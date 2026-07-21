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
)

// Entry is one link in the trust log. Genesis carries the initial signer set in
// Signers and is self-anchored (Signer is one of Signers). Every other entry has
// Prev = the previous entry's hash and Signer = a currently-trusted signer.
type Entry struct {
	Kind         Kind
	Prev         []byte   // hash of the previous entry; nil for genesis
	Signers      [][]byte // genesis only: initial trusted signer pubkeys
	Disablements [][]byte // genesis only: Argon2id commitments of disablement secrets
	Key          []byte   // add/remove-signer or authorize/revoke-device target pubkey
	Signer       []byte   // pubkey of the signer that signed this entry
	Sig          []byte   // Ed25519 signature over sigBytes(entry)
}

func putField(buf *bytes.Buffer, b []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(b)))
	buf.Write(n[:])
	buf.Write(b)
}

// sigBytes is the deterministic encoding an entry's signature covers: every field
// EXCEPT Sig. Length-prefixed fields in a fixed order.
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
	return buf.Bytes()
}

// hashEntry is the chain hash of a full (signed) entry — it covers Sig, so Prev
// commits to the exact signed predecessor.
func hashEntry(e *Entry) []byte {
	var buf bytes.Buffer
	buf.Write(sigBytes(e))
	putField(&buf, e.Sig)
	sum := blake2s.Sum256(buf.Bytes())
	return sum[:]
}

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
