package trustlog

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"strings"

	bip39 "github.com/tyler-smith/go-bip39"
	"golang.org/x/crypto/blake2s"
)

// fingerprintWords encodes a 32-byte digest as a standard BIP39 mnemonic (24
// words including the checksum), so the human comparison covers the full 256-bit
// hash rather than a truncated prefix. The Flutter client uses the same BIP39
// wordlist, so the words match across argus and the app.
func fingerprintWords(digest []byte) []string {
	if len(digest) == 0 {
		return nil
	}
	m, err := bip39.NewMnemonic(digest)
	if err != nil {
		// Non-standard entropy length (never happens for a 32-byte hash); fall back
		// to hex so a caller still gets a stable, comparable string.
		return []string{hex.EncodeToString(digest)}
	}
	return strings.Split(m, " ")
}

// SignerSetFingerprint is the human-verifiable BIP39 fingerprint of the trusted
// signer set: BLAKE2s-256 over length-prefixed, byte-sorted signer pubkeys, then
// BIP39-encoded. Deterministic and identical to the Flutter client.
func SignerSetFingerprint(signers [][]byte) []string {
	sorted := make([][]byte, len(signers))
	copy(sorted, signers)
	sort.Slice(sorted, func(i, j int) bool { return bytes.Compare(sorted[i], sorted[j]) < 0 })
	var buf bytes.Buffer
	var n [4]byte
	for _, s := range sorted {
		binary.BigEndian.PutUint32(n[:], uint32(len(s)))
		buf.Write(n[:])
		buf.Write(s)
	}
	sum := blake2s.Sum256(buf.Bytes())
	return fingerprintWords(sum[:])
}

// HashFingerprint is the human-verifiable BIP39 fingerprint of a raw 32-byte hash
// (a genesis or a chain tip), matching SignerSetFingerprint's encoding so the words
// are consistent across argus and the Flutter client.
func HashFingerprint(hash []byte) []string { return fingerprintWords(hash) }
