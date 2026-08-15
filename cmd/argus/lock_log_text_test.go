package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/keyfmt"
)

// The whole point of printing entry hashes is that one can be pasted into
// revoke-signer --fork-from, which parses with exactly this call.
func TestLockLogEntryHashesParseBackAsForkPoints(t *testing.T) {
	entries := []api.LockLogEntry{
		{Index: 0, Kind: "genesis", Hash: testGenesis(0xE0)},
		{Index: 1, Kind: "authorize-device", Hash: testGenesis(0xE1)},
		{Index: 2, Kind: "add-signer", Hash: testGenesis(0xE2)},
	}
	for _, e := range entries {
		s := entryHashString(e)
		got, err := keyfmt.DecodeAny(s, keyfmt.Tip, keyfmt.Genesis)
		if err != nil {
			t.Fatalf("entry %d: %q is not a usable fork point: %v", e.Index, s, err)
		}
		if !bytes.Equal(got, e.Hash) {
			t.Errorf("entry %d: round trip = %x, want %x", e.Index, got, e.Hash)
		}
	}
}

// Entry 0 is the trust root and every later entry is a mid-chain hash; the prefixes
// must say which is which.
func TestEntryHashStringTagsGenesisAndLaterEntriesDifferently(t *testing.T) {
	genesis := entryHashString(api.LockLogEntry{Index: 0, Hash: testGenesis(0xE0)})
	if !strings.HasPrefix(genesis, keyfmt.Genesis.Prefix()) {
		t.Errorf("entry 0 = %q, want a %s prefix", genesis, keyfmt.Genesis.Prefix())
	}
	later := entryHashString(api.LockLogEntry{Index: 1, Hash: testGenesis(0xE1)})
	if !strings.HasPrefix(later, keyfmt.Tip.Prefix()) {
		t.Errorf("entry 1 = %q, want a %s prefix", later, keyfmt.Tip.Prefix())
	}
}

func TestLockLogTrailerNamesGenesisTipLengthAndSigners(t *testing.T) {
	res := api.LockLogResult{
		Entries: []api.LockLogEntry{
			{Index: 0, Kind: "genesis", Hash: testGenesis(0xE0)},
			{Index: 1, Kind: "authorize-device", Hash: testGenesis(0xE1)},
		},
		Tip:     testGenesis(0xE1),
		Signers: [][]byte{testGenesis(0xF1), testGenesis(0xF2)},
	}
	out := lockLogTrailer(res)
	for _, want := range []string{
		"genesis: " + keyfmt.Genesis.Encode(testGenesis(0xE0)),
		"tip:     " + keyfmt.Tip.Encode(testGenesis(0xE1)),
		"length:  2 entries",
		"signers: 2  fingerprint: " + signerSetFingerprintOf(res.Signers),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("trailer must contain %q:\n%s", want, out)
		}
	}
}

// The old label called the signer-set fingerprint a tip fingerprint. Two chains with
// the same signers and different tips fingerprint identically, so the label lied.
func TestLockLogTrailerDoesNotCallTheSignerSetATip(t *testing.T) {
	out := lockLogTrailer(api.LockLogResult{
		Entries: []api.LockLogEntry{{Index: 0, Kind: "genesis", Hash: testGenesis(0xE0)}},
		Tip:     testGenesis(0xE0),
		Signers: [][]byte{testGenesis(0xF1)},
	})
	if strings.Contains(out, "tip fingerprint") {
		t.Errorf("trailer must not label the signer set a tip fingerprint:\n%s", out)
	}
}
