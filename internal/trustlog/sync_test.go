package trustlog

import (
	"bytes"
	"sync"
	"testing"
)

// buildSyncChain returns a marshaled genesis[+authorize] chain and the pieces the
// tests assert against.
func buildSyncChain(t *testing.T, withDevice bool) (chain []byte, genesisHash []byte, signer SignerKey, device []byte) {
	t.Helper()
	signer, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	log, err := NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisHash = log.Tip()
	device = bytes.Repeat([]byte{0xAB}, 32)
	if withDevice {
		if err := log.AuthorizeDevice(device, signer); err != nil {
			t.Fatalf("AuthorizeDevice: %v", err)
		}
	}
	return MarshalChain(log.Entries()), genesisHash, signer, device
}

func TestSyncStoreIngestReportsAdvance(t *testing.T) {
	genChain, head, signer, device := buildSyncChain(t, false)
	s := NewSyncStore(head)

	changed, err := s.Ingest(genChain)
	if err != nil || !changed {
		t.Fatalf("first ingest: changed=%v err=%v", changed, err)
	}
	// Re-ingesting the identical chain is a no-op: no advance, no error.
	changed, err = s.Ingest(genChain)
	if err != nil || changed {
		t.Fatalf("identical re-ingest: changed=%v err=%v", changed, err)
	}

	// Extend the chain and confirm advance + query reflects the new device.
	log, err := Load(mustUnmarshal(t, genChain))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := log.AuthorizeDevice(device, signer); err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	changed, err = s.Ingest(MarshalChain(log.Entries()))
	if err != nil || !changed {
		t.Fatalf("extend ingest: changed=%v err=%v", changed, err)
	}
	if !s.DeviceAuthorized(device) {
		t.Fatal("device should be authorized after extend")
	}
}

func TestSyncStoreRejectsWrongGenesis(t *testing.T) {
	_, headA, _, _ := buildSyncChain(t, false)
	chainB, _, _, _ := buildSyncChain(t, false)
	s := NewSyncStore(headA)
	if changed, err := s.Ingest(chainB); err == nil || changed {
		t.Fatalf("wrong-genesis ingest should error: changed=%v err=%v", changed, err)
	}
}

func TestSyncStoreConcurrent(t *testing.T) {
	genChain, head, _, _ := buildSyncChain(t, false)
	s := NewSyncStore(head)
	if _, err := s.Ingest(genChain); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = s.Ingest(genChain) // idempotent no-op
				_ = s.Tip()
				_ = s.Bytes()
				_ = s.SignerTrusted(nil)
				_ = s.DeviceAuthorized(nil)
			}
		}()
	}
	wg.Wait()
}

func mustUnmarshal(t *testing.T, b []byte) []Entry {
	t.Helper()
	e, err := UnmarshalChain(b)
	if err != nil {
		t.Fatalf("UnmarshalChain: %v", err)
	}
	return e
}

// TestSyncTamperedChainRejected: a byte-flipped chain is rejected by Load before
// fork-choice runs. A gateway holding no signer keys cannot forge valid signatures,
// so any tampered chain is caught at decode/verify time.
func TestSyncTamperedChainRejected(t *testing.T) {
	genChain, gh, _, _ := buildSyncChain(t, true)
	s := NewSyncStore(gh)
	changed, err := s.Ingest(genChain)
	if err != nil || !changed {
		t.Fatalf("initial ingest: changed=%v err=%v", changed, err)
	}
	tipBefore := s.Tip()

	tampered := append([]byte(nil), genChain...)
	tampered[len(tampered)-1] ^= 0xff

	changed, err = s.Ingest(tampered)
	if err == nil {
		t.Fatal("tampered chain must be rejected with an error")
	}
	if changed {
		t.Fatal("tampered chain ingest must not report changed")
	}
	if !bytes.Equal(s.Tip(), tipBefore) {
		t.Fatal("tip must be unchanged after rejecting a tampered chain")
	}
}

// TestSyncStalePrefixIsNoop: a stale strict-prefix chain (shorter, no divergence)
// is a silent no-op — changed=false, err=nil, state preserved. The gateway cannot
// roll back state by replaying an older chain snapshot.
func TestSyncStalePrefixIsNoop(t *testing.T) {
	gh, short, extended, dev := buildStoreChain(t)
	s := NewSyncStore(gh)

	changed, err := s.Ingest(extended)
	if err != nil || !changed {
		t.Fatalf("initial ingest of extended chain: changed=%v err=%v", changed, err)
	}
	tipBefore := s.Tip()

	changed, err = s.Ingest(short)
	if err != nil {
		t.Fatalf("strict-prefix ingest must not error: %v", err)
	}
	if changed {
		t.Fatal("strict-prefix ingest must not report changed")
	}
	if s.DeviceAuthorized(dev) {
		t.Error("device must remain revoked after no-op prefix ingest")
	}
	if !bytes.Equal(s.Tip(), tipBefore) {
		t.Error("tip must be unchanged after no-op prefix ingest")
	}
}

// TestSyncAdoptsValidCoSignedRevoke: a genuine co-signed signer-revocation branch
// IS adopted (changed=true) even though it is shorter than the current attacker
// branch. changed is tip-based — not length-based — so a shorter chain with a new
// tip correctly reports changed=true.
func TestSyncAdoptsValidCoSignedRevoke(t *testing.T) {
	cur, cand, gh, c := forkFixtureCompromisedSigner(t)
	s := NewSyncStore(gh)

	changed, err := s.Ingest(cur)
	if err != nil || !changed {
		t.Fatalf("adopt attacker branch: changed=%v err=%v", changed, err)
	}
	if !s.SignerTrusted(c.Public) {
		t.Fatal("c must be trusted before the co-signed revoke is ingested")
	}

	changed, err = s.Ingest(cand)
	if err != nil {
		t.Fatalf("co-signed revoke branch must be adopted: %v", err)
	}
	if !changed {
		t.Fatal("changed must be true when adopting a co-signed revoke (tip changes even if chain is shorter)")
	}
	if s.SignerTrusted(c.Public) {
		t.Fatal("c must not be trusted after co-signed revoke is adopted")
	}
	if s.DeviceAuthorized(bytes.Repeat([]byte{0xA1}, 32)) || s.DeviceAuthorized(bytes.Repeat([]byte{0xA2}, 32)) {
		t.Fatal("attacker devices must be deauthorized after adopting the revoke branch")
	}
	if !bytes.Equal(s.Bytes(), cand) {
		t.Fatal("store must hold the co-signed revoke branch")
	}
}

// TestSyncRejectsGatewayRollbackAfterRevoke: after a co-signed signer revocation
// is adopted, a malicious gateway that re-offers the old pre-revoke chain cannot
// win fork-choice — it holds no signer keys and cannot produce a co-signed branch
// that outweighs the honest revocation. changed=false, state preserved.
func TestSyncRejectsGatewayRollbackAfterRevoke(t *testing.T) {
	cur, cand, gh, c := forkFixtureCompromisedSigner(t)
	s := NewSyncStore(gh)

	changed, err := s.Ingest(cand)
	if err != nil || !changed {
		t.Fatalf("adopt co-signed revoke: changed=%v err=%v", changed, err)
	}
	tipAfterRevoke := s.Tip()

	// Gateway re-offers the old attacker chain (longer, single-signed by c).
	changed, err = s.Ingest(cur)
	if err != nil {
		t.Fatalf("re-offering old chain must not error (fork resolves deterministically): %v", err)
	}
	if changed {
		t.Fatal("re-offering the old pre-revoke chain must not change state")
	}
	if s.SignerTrusted(c.Public) {
		t.Fatal("revoked signer c must remain untrusted after rollback attempt")
	}
	if s.DeviceAuthorized(bytes.Repeat([]byte{0xA1}, 32)) || s.DeviceAuthorized(bytes.Repeat([]byte{0xA2}, 32)) {
		t.Fatal("attacker devices must remain deauthorized after rollback attempt")
	}
	if !bytes.Equal(s.Tip(), tipAfterRevoke) {
		t.Fatal("tip must be unchanged after rollback attempt")
	}
	if !bytes.Equal(s.Bytes(), cand) {
		t.Fatal("store must still hold the co-signed revoke branch after rollback attempt")
	}
}
