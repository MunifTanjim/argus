package trustlog

import (
	"bytes"
	"sync"
	"testing"
)

// buildSyncChain returns a marshaled genesis[+authorize] chain and the pieces the
// tests assert against.
func buildSyncChain(t *testing.T, withDevice bool) (chain []byte, genesisHead []byte, signer SignerKey, device []byte) {
	t.Helper()
	signer, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	log, err := NewGenesis([][]byte{signer.Public}, signer)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisHead = log.Head()
	device = bytes.Repeat([]byte{0xAB}, 32)
	if withDevice {
		if err := log.AuthorizeDevice(device, signer); err != nil {
			t.Fatalf("AuthorizeDevice: %v", err)
		}
	}
	return MarshalChain(log.Entries()), genesisHead, signer, device
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
				_ = s.Head()
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
