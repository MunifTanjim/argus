package main

import (
	"bytes"
	"strings"
	"testing"
)

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
