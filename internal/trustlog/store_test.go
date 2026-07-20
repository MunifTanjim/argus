package trustlog

import (
	"bytes"
	"testing"
)

// buildStoreChain returns a genesis head + two chain snapshots (short then extended).
func buildStoreChain(t *testing.T) (genesisHead []byte, short, extended []byte, dev []byte) {
	t.Helper()
	s, _ := GenerateSigner()
	l, err := NewGenesis([][]byte{s.Public}, s)
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	gh := l.Head()
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
	gh, short, extended, _ := buildStoreChain(t)
	st := NewStore(gh)
	if err := st.Ingest(extended); err != nil {
		t.Fatalf("ingest extended: %v", err)
	}
	// A shorter (stale) chain must be rejected — the rollback defense.
	if err := st.Ingest(short); err == nil {
		t.Error("Ingest must reject a shorter (rolled-back) chain")
	}
}

func TestStoreRejectsWrongGenesis(t *testing.T) {
	gh, short, _, _ := buildStoreChain(t)
	_ = gh
	// A store pinned to a DIFFERENT genesis must reject this chain.
	st := NewStore([]byte("some-other-genesis-head-32bytes!"))
	if err := st.Ingest(short); err == nil {
		t.Error("Ingest must reject a chain whose genesis != pinned head")
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

func TestStoreRejectsGenuineFork(t *testing.T) {
	s, _ := GenerateSigner()
	la, err := NewGenesis([][]byte{s.Public}, s)
	if err != nil {
		t.Fatalf("genesis A: %v", err)
	}
	gh := la.Head()
	if err := la.AuthorizeDevice([]byte("dev-A"), s); err != nil {
		t.Fatalf("authorize A: %v", err)
	}
	// Same genesis (deterministic), divergent history: authorize a different device.
	lb, err := NewGenesis([][]byte{s.Public}, s)
	if err != nil {
		t.Fatalf("genesis B: %v", err)
	}
	if string(lb.Head()) != string(gh) {
		t.Fatal("expected identical (deterministic) genesis for both logs")
	}
	if err := lb.AuthorizeDevice([]byte("dev-B"), s); err != nil {
		t.Fatalf("authorize B: %v", err)
	}

	st := NewStore(gh)
	if err := st.Ingest(MarshalChain(la.Entries())); err != nil {
		t.Fatalf("ingest A: %v", err)
	}
	if err := st.Ingest(MarshalChain(lb.Entries())); err == nil {
		t.Error("Ingest must reject a same-genesis fork that diverges from current")
	}
	if !st.DeviceAuthorized([]byte("dev-A")) {
		t.Error("store must remain on chain A after rejecting the fork")
	}
	if st.DeviceAuthorized([]byte("dev-B")) {
		t.Error("forked chain B must not have been adopted")
	}
}

func TestStoreIngestIdenticalIsNoop(t *testing.T) {
	s, _ := GenerateSigner()
	l, err := NewGenesis([][]byte{s.Public}, s)
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	gh := l.Head()
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
