package trustlog

import (
	"bytes"
	"sync"
	"testing"
)

// lockedSyncStore returns a SyncStore holding a genesis (signer s1) chain, plus s1.
func lockedSyncStore(t *testing.T) (*SyncStore, SignerKey) {
	t.Helper()
	s1, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	log, err := NewGenesis([][]byte{s1.Public}, s1, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	ss := NewSyncStore(log.Head())
	if _, err := ss.Ingest(MarshalChain(log.Entries())); err != nil {
		t.Fatalf("Ingest genesis: %v", err)
	}
	return ss, s1
}

func TestSyncStoreAuthorizeAndRevoke(t *testing.T) {
	ss, s1 := lockedSyncStore(t)
	dev := bytes.Repeat([]byte{0x5A}, 32)

	changed, err := ss.AuthorizeDevice(dev, s1)
	if err != nil || !changed {
		t.Fatalf("authorize: changed=%v err=%v", changed, err)
	}
	if !ss.DeviceAuthorized(dev) {
		t.Fatal("device should be authorized")
	}

	changed, err = ss.RevokeDevice(dev, s1)
	if err != nil || !changed {
		t.Fatalf("revoke: changed=%v err=%v", changed, err)
	}
	if ss.DeviceAuthorized(dev) {
		t.Fatal("device should be revoked")
	}
}

func TestSyncStoreAppendRejectsUntrustedSigner(t *testing.T) {
	ss, _ := lockedSyncStore(t)
	rogue, _ := GenerateSigner() // not in the signer set
	dev := bytes.Repeat([]byte{0x5A}, 32)

	headBefore := ss.Head()
	if changed, err := ss.AuthorizeDevice(dev, rogue); err == nil || changed {
		t.Fatalf("untrusted signer should be rejected: changed=%v err=%v", changed, err)
	}
	if !bytes.Equal(ss.Head(), headBefore) {
		t.Fatal("state must be unchanged after a rejected append")
	}
	if ss.DeviceAuthorized(dev) {
		t.Fatal("device must not be authorized after a rejected append")
	}
}

func TestSyncStoreDisable(t *testing.T) {
	s, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	secret, err := GenerateDisablementSecret()
	if err != nil {
		t.Fatalf("GenerateDisablementSecret: %v", err)
	}
	commitment := DisablementCommitment(secret)
	log, err := NewGenesis([][]byte{s.Public}, s, [][]byte{commitment})
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	genesisHead := log.Head()
	ss := NewSyncStore(genesisHead)
	if _, err := ss.Ingest(MarshalChain(log.Entries())); err != nil {
		t.Fatalf("Ingest genesis: %v", err)
	}

	if ss.Disabled() {
		t.Fatal("Disabled() should be false before Disable")
	}

	changed, err := ss.Disable(secret, s)
	if err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if !changed {
		t.Error("Disable should report changed=true")
	}
	if !ss.Disabled() {
		t.Error("Disabled() should be true after Disable")
	}
}

func TestSyncStoreDisableUnknownSecretErrors(t *testing.T) {
	s, err := GenerateSigner()
	if err != nil {
		t.Fatalf("GenerateSigner: %v", err)
	}
	secret, err := GenerateDisablementSecret()
	if err != nil {
		t.Fatalf("GenerateDisablementSecret: %v", err)
	}
	commitment := DisablementCommitment(secret)
	log, err := NewGenesis([][]byte{s.Public}, s, [][]byte{commitment})
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	ss := NewSyncStore(log.Head())
	if _, err := ss.Ingest(MarshalChain(log.Entries())); err != nil {
		t.Fatalf("Ingest genesis: %v", err)
	}

	wrongSecret, err := GenerateDisablementSecret()
	if err != nil {
		t.Fatalf("GenerateDisablementSecret: %v", err)
	}
	changed, err := ss.Disable(wrongSecret, s)
	if err == nil {
		t.Error("Disable with unknown secret should return an error")
	}
	if changed {
		t.Error("Disable with unknown secret should not report changed=true")
	}
	if ss.Disabled() {
		t.Error("Disabled() should remain false after a rejected Disable")
	}
}

// TestSyncStoreConcurrentAppendIngest verifies that concurrent AuthorizeDevice,
// RevokeDevice, and Ingest calls on the same SyncStore are -race clean. The shared
// mutex guarantees no data race; this test backs that claim.
func TestSyncStoreConcurrentAppendIngest(t *testing.T) {
	ss, s1 := lockedSyncStore(t)
	dev := bytes.Repeat([]byte{0x7F}, 32)

	const iters = 50
	var wg sync.WaitGroup

	// goroutine 1: repeatedly authorize
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range iters {
			ss.AuthorizeDevice(dev, s1) //nolint:errcheck
		}
	}()

	// goroutine 2: repeatedly revoke
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range iters {
			ss.RevokeDevice(dev, s1) //nolint:errcheck
		}
	}()

	// goroutine 3: repeatedly ingest the current chain (no-op re-ingest)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range iters {
			chain := ss.Bytes()
			if chain != nil {
				ss.Ingest(chain) //nolint:errcheck
			}
		}
	}()

	wg.Wait()
}
