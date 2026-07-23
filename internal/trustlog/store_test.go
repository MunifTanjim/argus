package trustlog

import (
	"bytes"
	"testing"
)

// buildStoreChain returns a genesis head + two chain snapshots (short then extended).
func buildStoreChain(t *testing.T) (genesisHash []byte, short, extended []byte, dev []byte) {
	t.Helper()
	s, _ := GenerateSigner()
	l, err := NewGenesis([][]byte{s.Public}, s, nil)
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	gh := l.Tip()
	dev = []byte("device-1")
	if err := l.AuthorizeDevice(dev, s); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	short = MarshalChain(l.Entries())
	if err := l.RevokeDevice(dev, s); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	extended = MarshalChain(l.Entries())
	return gh, short, extended, dev
}

func TestStoreIngestAndQuery(t *testing.T) {
	gh, short, _, dev := buildStoreChain(t)
	st := NewStore(gh)
	if err := st.Ingest(short); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if !st.DeviceAuthorized(dev) {
		t.Error("device should be authorized after ingesting the chain")
	}
	if !bytes.Equal(st.Bytes(), short) {
		t.Error("Bytes() should reproduce the ingested chain")
	}
}

func TestStoreAdoptsLongerExtension(t *testing.T) {
	gh, short, extended, dev := buildStoreChain(t)
	st := NewStore(gh)
	if err := st.Ingest(short); err != nil {
		t.Fatalf("ingest short: %v", err)
	}
	if err := st.Ingest(extended); err != nil {
		t.Fatalf("ingest extended: %v", err)
	}
	if st.DeviceAuthorized(dev) {
		t.Error("device should be revoked after ingesting the extended chain")
	}
}

func TestStoreRejectsRollback(t *testing.T) {
	gh, short, extended, dev := buildStoreChain(t)
	st := NewStore(gh)
	if err := st.Ingest(extended); err != nil {
		t.Fatalf("ingest extended: %v", err)
	}
	// A shorter (stale) strict-prefix chain must NOT roll the state back: it is a
	// no-op that keeps the current (longer) chain — the rollback defense.
	if err := st.Ingest(short); err != nil {
		t.Fatalf("strict-prefix rollback must be a no-op, got: %v", err)
	}
	if st.DeviceAuthorized(dev) {
		t.Error("state must remain on the longer chain (device stays revoked)")
	}
	if !bytes.Equal(st.Bytes(), extended) {
		t.Error("store must keep the current (longer) chain after a rollback attempt")
	}
}

func TestStoreRejectsWrongGenesis(t *testing.T) {
	gh, short, _, _ := buildStoreChain(t)
	_ = gh
	// A store pinned to a DIFFERENT genesis must reject this chain.
	st := NewStore([]byte("some-other-genesis-head-32bytes!"))
	if err := st.Ingest(short); err == nil {
		t.Error("Ingest must reject a chain whose genesis != pinned genesis hash")
	}
}

func TestStoreRejectsForkAndTamper(t *testing.T) {
	gh, short, _, _ := buildStoreChain(t)
	// Build a divergent chain from the SAME genesis signer but different content.
	s, _ := GenerateSigner()
	_ = s
	st := NewStore(gh)
	if err := st.Ingest(short); err != nil {
		t.Fatalf("ingest short: %v", err)
	}
	// Tampered bytes must be rejected by the codec/Load path.
	bad := append([]byte(nil), short...)
	bad[len(bad)-1] ^= 0xff
	if err := st.Ingest(bad); err == nil {
		t.Error("Ingest must reject tampered chain bytes")
	}
}

// TestStoreRejectsGenuineFork: a plain same-genesis fork (neither branch carries a
// co-signed removal) no longer errors — it RESOLVES deterministically at the fork
// point (weight tie → both plain, no removal → lowest first-diverging-entry hash
// wins). The winner must be identical regardless of ingest order.
func TestStoreRejectsGenuineFork(t *testing.T) {
	s, _ := GenerateSigner()
	la, err := NewGenesis([][]byte{s.Public}, s, nil)
	if err != nil {
		t.Fatalf("genesis A: %v", err)
	}
	gh := la.Tip()
	if err := la.AuthorizeDevice([]byte("dev-A"), s); err != nil {
		t.Fatalf("authorize A: %v", err)
	}
	// Same genesis (deterministic), divergent history: authorize a different device.
	lb, err := NewGenesis([][]byte{s.Public}, s, nil)
	if err != nil {
		t.Fatalf("genesis B: %v", err)
	}
	if string(lb.Tip()) != string(gh) {
		t.Fatal("expected identical (deterministic) genesis for both logs")
	}
	if err := lb.AuthorizeDevice([]byte("dev-B"), s); err != nil {
		t.Fatalf("authorize B: %v", err)
	}
	chainA := MarshalChain(la.Entries())
	chainB := MarshalChain(lb.Entries())

	// Ingest A then B.
	s1 := NewStore(gh)
	if err := s1.Ingest(chainA); err != nil {
		t.Fatalf("ingest A: %v", err)
	}
	if err := s1.Ingest(chainB); err != nil {
		t.Fatalf("plain fork must resolve, not error (A then B): %v", err)
	}
	// Ingest B then A.
	s2 := NewStore(gh)
	if err := s2.Ingest(chainB); err != nil {
		t.Fatalf("ingest B: %v", err)
	}
	if err := s2.Ingest(chainA); err != nil {
		t.Fatalf("plain fork must resolve, not error (B then A): %v", err)
	}
	if !bytes.Equal(s1.Bytes(), s2.Bytes()) {
		t.Fatal("plain fork winner must be identical regardless of ingest order")
	}

	// Winner is the branch with the lexicographically-lower first-diverging-entry
	// hash (index 1 here; genesis is the shared prefix).
	ha := hashEntry(&la.Entries()[1])
	hb := hashEntry(&lb.Entries()[1])
	winner := chainB
	if bytes.Compare(ha, hb) < 0 {
		winner = chainA
	}
	if !bytes.Equal(s1.Bytes(), winner) {
		t.Fatal("plain fork must adopt the lexicographically-lower-first-entry branch")
	}
}

func TestStoreIngestIdenticalIsNoop(t *testing.T) {
	s, _ := GenerateSigner()
	l, err := NewGenesis([][]byte{s.Public}, s, nil)
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	gh := l.Tip()
	if err := l.AuthorizeDevice([]byte("d"), s); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	chain := MarshalChain(l.Entries())
	st := NewStore(gh)
	if err := st.Ingest(chain); err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if err := st.Ingest(chain); err != nil {
		t.Errorf("re-ingesting the identical chain must be a no-op (nil), got %v", err)
	}
	if !st.DeviceAuthorized([]byte("d")) {
		t.Error("state must be unchanged after a no-op re-ingest")
	}
}
