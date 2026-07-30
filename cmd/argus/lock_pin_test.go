package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/trustlog"
	"github.com/MunifTanjim/argus/internal/trustpin"
)

func makeTestChainBytes(t *testing.T) []byte {
	t.Helper()
	sk, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	log, err := trustlog.NewGenesis([][]byte{sk.Public}, sk, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	return trustlog.MarshalChain(log.Entries())
}

func TestDistinctGenesisFromChains(t *testing.T) {
	a := testGenesis(0x01)
	b := testGenesis(0x02)

	got := distinctGenesis([][]byte{a, a, b})

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 distinct", len(got))
	}
	if !bytes.Equal(got[0], a) || !bytes.Equal(got[1], b) {
		t.Fatal("distinctGenesis must preserve first-seen order")
	}
}

func TestConfirmGenesisAcceptsY(t *testing.T) {
	var out bytes.Buffer
	ok, err := confirmGenesis(strings.NewReader("y\n"), &out, testGenesis(0x09))
	if err != nil {
		t.Fatalf("confirmGenesis: %v", err)
	}
	if !ok {
		t.Fatal("y should accept")
	}
	if !strings.Contains(out.String(), "algol") {
		t.Fatalf("prompt must show the word fingerprint, got: %q", out.String())
	}
}

func TestConfirmGenesisAcceptsYes(t *testing.T) {
	var out bytes.Buffer
	ok, err := confirmGenesis(strings.NewReader("yes\n"), &out, testGenesis(0x09))
	if err != nil {
		t.Fatalf("confirmGenesis: %v", err)
	}
	if !ok {
		t.Fatal("yes should accept")
	}
}

func TestConfirmGenesisDefaultsToNo(t *testing.T) {
	var out bytes.Buffer
	for _, in := range []string{"\n", "n\n", "no\n", ""} {
		ok, err := confirmGenesis(strings.NewReader(in), &out, testGenesis(0x09))
		if err != nil {
			t.Fatalf("confirmGenesis(%q): %v", in, err)
		}
		if ok {
			t.Fatalf("input %q must not accept", in)
		}
	}
}

func TestGuardExistingPinNoPin(t *testing.T) {
	pf := trustpin.New(filepath.Join(t.TempDir(), "trustlog-genesis"))
	if err := guardExistingPin(pf, testGenesis(0x01)); err != nil {
		t.Fatalf("no existing pin should pass: %v", err)
	}
}

func TestGuardExistingPinIdentical(t *testing.T) {
	pf := trustpin.New(filepath.Join(t.TempDir(), "trustlog-genesis"))
	g := testGenesis(0x01)
	if err := pf.Save(g); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := guardExistingPin(pf, g); err != nil {
		t.Fatalf("identical pin should be idempotent: %v", err)
	}
}

func TestGuardExistingPinDifferent(t *testing.T) {
	pf := trustpin.New(filepath.Join(t.TempDir(), "trustlog-genesis"))
	if err := pf.Save(testGenesis(0x01)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	err := guardExistingPin(pf, testGenesis(0x02))
	if err == nil {
		t.Fatal("different pin must return an error")
	}
	if !strings.Contains(err.Error(), "lock unpin") {
		t.Fatalf("error must mention lock unpin, got: %v", err)
	}
}

func TestResolveGenesisNoChains(t *testing.T) {
	g, err := resolveGenesis(nil)
	if err != nil || g != nil {
		t.Fatalf("no chains → want (nil, nil), got (%v, %v)", g, err)
	}
}

func TestResolveGenesisGarbageChains(t *testing.T) {
	_, err := resolveGenesis([][]byte{{1, 2, 3}, {4, 5, 6}})
	if err == nil {
		t.Fatal("garbage chains must return an error")
	}
	if !strings.Contains(err.Error(), "2 chain") {
		t.Fatalf("error must mention count, got: %v", err)
	}
}

func TestResolveGenesisMultipleRoots(t *testing.T) {
	chain1 := makeTestChainBytes(t)
	chain2 := makeTestChainBytes(t)
	_, err := resolveGenesis([][]byte{chain1, chain2})
	if err == nil {
		t.Fatal("two distinct roots must return an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "2 different trust roots") {
		t.Fatalf("error must say 2 different trust roots, got: %v", msg)
	}
	// Both genesis hashes — encoded form and word fingerprint — must appear so the
	// operator can identify which root is theirs and pass it explicitly.
	for _, chain := range [][]byte{chain1, chain2} {
		entries, _ := trustlog.UnmarshalChain(chain)
		genesis := trustlog.HashEntry(&entries[0])
		enc := trustpin.Encode(genesis)
		fp := fingerprintOf(genesis)
		if !strings.Contains(msg, enc) {
			t.Errorf("error must contain %s, got: %v", enc, msg)
		}
		if !strings.Contains(msg, fp) {
			t.Errorf("error must contain fingerprint %q, got: %v", fp, msg)
		}
	}
}

func TestResolveGenesisSingleRoot(t *testing.T) {
	chain := makeTestChainBytes(t)
	g, err := resolveGenesis([][]byte{chain, chain})
	if err != nil {
		t.Fatalf("single root: %v", err)
	}
	if len(g) != trustpin.GenesisLen {
		t.Fatalf("genesis len = %d, want %d", len(g), trustpin.GenesisLen)
	}
}
