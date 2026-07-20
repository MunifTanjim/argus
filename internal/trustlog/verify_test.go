package trustlog

import (
	"testing"
)

func buildChain(t *testing.T) (*Log, SignerKey) {
	t.Helper()
	s, _ := GenerateSigner()
	l, err := NewGenesis([][]byte{s.Public}, s)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	if err := l.AuthorizeDevice([]byte("dev-A"), s); err != nil {
		t.Fatalf("authorize A: %v", err)
	}
	if err := l.AuthorizeDevice([]byte("dev-B"), s); err != nil {
		t.Fatalf("authorize B: %v", err)
	}
	if err := l.RevokeDevice([]byte("dev-A"), s); err != nil {
		t.Fatalf("revoke A: %v", err)
	}
	return l, s
}

func TestLoadReproducesState(t *testing.T) {
	l, _ := buildChain(t)
	l2, err := Load(l.Entries())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if l2.DeviceAuthorized([]byte("dev-A")) {
		t.Error("dev-A should be revoked after replay")
	}
	if !l2.DeviceAuthorized([]byte("dev-B")) {
		t.Error("dev-B should be authorized after replay")
	}
	if string(l2.Head()) != string(l.Head()) {
		t.Error("replayed head must match the original")
	}
}

func TestLoadRejectsTamperedEntry(t *testing.T) {
	l, _ := buildChain(t)
	entries := l.Entries()
	entries[2].Key = []byte("dev-EVIL") // flip an authorize target without re-signing
	if _, err := Load(entries); err == nil {
		t.Error("Load must reject a tampered entry")
	}
}

func TestLoadRejectsReorder(t *testing.T) {
	l, _ := buildChain(t)
	entries := l.Entries()
	entries[1], entries[2] = entries[2], entries[1] // swap two deltas -> Prev links break
	if _, err := Load(entries); err == nil {
		t.Error("Load must reject reordered entries (broken hash chain)")
	}
}

func TestLoadRejectsRollback(t *testing.T) {
	l, _ := buildChain(t)
	entries := l.Entries()
	truncated := entries[:len(entries)-1] // drop the revoke -> a stale (pre-revocation) view
	l2, err := Load(truncated)
	if err != nil {
		t.Fatalf("Load(truncated): %v", err)
	}
	// A truncated chain is internally valid but has a DIFFERENT head; a node pins
	// the real head out-of-band, so the rollback is detected by the head mismatch.
	if string(l2.Head()) == string(l.Head()) {
		t.Error("a rolled-back chain must have a different head than the full chain")
	}
	// And it exposes the stale state the attacker wanted (dev-A still authorized) —
	// which is exactly why head pinning is the defense.
	if !l2.DeviceAuthorized([]byte("dev-A")) {
		t.Error("truncated-before-revoke chain should still show dev-A authorized")
	}
}

func TestLoadRejectsForgedSignerEdit(t *testing.T) {
	l, s := buildChain(t)
	rogue, _ := GenerateSigner()
	// Append a rogue-signed authorize onto a copy of the chain's entries.
	entries := l.Entries()
	e := Entry{Kind: KindAuthorizeDevice, Prev: hashEntry(&entries[len(entries)-1]), Key: []byte("dev-X")}
	sign(&e, rogue) // signed by a NON-trusted signer
	entries = append(entries, e)
	if _, err := Load(entries); err == nil {
		t.Error("Load must reject an entry signed by an untrusted signer")
	}
	_ = s
}

func TestAddSignerThenNewSignerCanAuthorize(t *testing.T) {
	s1, _ := GenerateSigner()
	l, _ := NewGenesis([][]byte{s1.Public}, s1)
	s2, _ := GenerateSigner()
	if err := l.AddSigner(s2.Public, s1); err != nil {
		t.Fatalf("AddSigner: %v", err)
	}
	if !l.SignerTrusted(s2.Public) {
		t.Fatal("s2 not trusted after AddSigner")
	}
	if err := l.AuthorizeDevice([]byte("d"), s2); err != nil {
		t.Fatalf("s2 authorize: %v", err)
	}
	if !l.DeviceAuthorized([]byte("d")) {
		t.Error("device authorized by the newly-added signer should be authorized")
	}
}

func TestRemoveLastSignerRejected(t *testing.T) {
	s, _ := GenerateSigner()
	l, _ := NewGenesis([][]byte{s.Public}, s)
	if err := l.RemoveSigner(s.Public, s); err == nil {
		t.Error("removing the last signer must be rejected")
	}
}

func TestEntriesDeepCopyIsolated(t *testing.T) {
	s, _ := GenerateSigner()
	l, _ := NewGenesis([][]byte{s.Public}, s)
	if err := l.AuthorizeDevice([]byte("dev-1"), s); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	head := string(l.Head())
	got := l.Entries()
	// Mutate the returned entries' byte fields; the log must be unaffected.
	for i := range got {
		for j := range got[i].Key {
			got[i].Key[j] ^= 0xff
		}
		for j := range got[i].Sig {
			got[i].Sig[j] ^= 0xff
		}
	}
	if string(l.Head()) != head {
		t.Error("mutating Entries() output changed the log head — not deep-copied")
	}
	if !l.DeviceAuthorized([]byte("dev-1")) {
		t.Error("mutating Entries() output corrupted the log's device state")
	}
	// The log must still re-Load cleanly (its stored entries weren't corrupted).
	if _, err := Load(l.Entries()); err != nil {
		t.Errorf("Load after external mutation failed: %v", err)
	}
}
