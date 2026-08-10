package trustlog

import (
	"math/rand"
	"testing"
)

// genResult is a generated, fully-valid chain plus the signer keys used to build
// it (so tests can extend it further with correctly-signed entries).
type genResult struct {
	entries []Entry
	signers []SignerKey // currently-trusted signer keys, index-stable with the log
}

// genChain builds a deterministic random valid chain: a genesis with 3 signers
// (so revoke-signer is exercisable) plus `ops` random mutations. It uses the Log
// mutation methods so every entry is correctly signed. It never produces an
// invalid chain (ops that would violate an invariant are skipped).
func genChain(t *testing.T, seed int64, ops int) genResult {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	s1, _ := GenerateSigner()
	s2, _ := GenerateSigner()
	s3, _ := GenerateSigner()
	keys := []SignerKey{s1, s2, s3}
	// NewGenesis(signers, by, disablements) returns a loaded *Log directly.
	l, err := NewGenesis([][]byte{s1.Public, s2.Public, s3.Public}, s1, nil)
	if err != nil {
		t.Fatalf("genChain genesis: %v", err)
	}
	devs := [][]byte{}
	for i := 0; i < ops; i++ {
		switch r.Intn(5) {
		case 0: // authorize a fresh device
			d, _ := GenerateSigner()
			if err := l.AuthorizeDevice(d.Public, keys[r.Intn(len(keys))]); err == nil {
				devs = append(devs, d.Public)
			}
		case 1: // revoke a known device
			if len(devs) > 0 {
				d := devs[r.Intn(len(devs))]
				_ = l.RevokeDevice(d, keys[r.Intn(len(keys))])
			}
		case 2: // add a signer
			ns, _ := GenerateSigner()
			if err := l.AddSigner(ns.Public, keys[r.Intn(len(keys))]); err == nil {
				keys = append(keys, ns)
			}
		case 3: // remove a signer (never the last); keep keys in sync
			if len(keys) > 1 {
				if err := l.RemoveSigner(keys[len(keys)-1].Public, keys[0]); err == nil {
					keys = keys[:len(keys)-1]
				}
			}
		case 4: // revoke a signer co-signed by all others (need ≥3 so 2 can co-sign 1 revocation)
			if len(keys) >= 3 {
				revokedIdx := len(keys) - 1
				revokedKey := keys[revokedIdx]
				coSigners := keys[:revokedIdx]
				var replacePub [][]byte
				var newKey *SignerKey
				if r.Intn(2) == 0 {
					ns, _ := GenerateSigner()
					replacePub = [][]byte{ns.Public}
					newKey = &ns
				}
				if err := l.RevokeSigner([][]byte{revokedKey.Public}, replacePub, coSigners); err == nil {
					keys = keys[:revokedIdx]
					if newKey != nil {
						keys = append(keys, *newKey)
					}
				}
			}
		}
	}
	return genResult{entries: l.Entries(), signers: keys}
}
