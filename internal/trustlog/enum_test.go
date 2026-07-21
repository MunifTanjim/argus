package trustlog

import (
	"bytes"
	"sort"
	"testing"
)

func TestLogSignersAndDevices(t *testing.T) {
	s1, _ := GenerateSigner()
	s2, _ := GenerateSigner()
	log, err := NewGenesis([][]byte{s1.Public}, s1, nil)
	if err != nil {
		t.Fatalf("NewGenesis: %v", err)
	}
	if err := log.AddSigner(s2.Public, s1); err != nil {
		t.Fatalf("AddSigner: %v", err)
	}
	devA := bytes.Repeat([]byte{0xA1}, 32)
	devB := bytes.Repeat([]byte{0xB2}, 32)
	if err := log.AuthorizeDevice(devA, s1); err != nil {
		t.Fatalf("AuthorizeDevice A: %v", err)
	}
	if err := log.AuthorizeDevice(devB, s1); err != nil {
		t.Fatalf("AuthorizeDevice B: %v", err)
	}

	signers := log.Signers()
	if len(signers) != 2 {
		t.Fatalf("signers = %d, want 2", len(signers))
	}
	if !containsBytes(signers, s1.Public) || !containsBytes(signers, s2.Public) {
		t.Fatal("signers missing an expected key")
	}
	// Sorted for stable output.
	if !sort.SliceIsSorted(signers, func(i, j int) bool { return bytes.Compare(signers[i], signers[j]) < 0 }) {
		t.Fatal("signers not sorted")
	}
	devices := log.Devices()
	if len(devices) != 2 || !containsBytes(devices, devA) || !containsBytes(devices, devB) {
		t.Fatalf("devices = %v", devices)
	}
	// Sorted for stable output.
	if !sort.SliceIsSorted(devices, func(i, j int) bool { return bytes.Compare(devices[i], devices[j]) < 0 }) {
		t.Fatal("devices not sorted")
	}
	// Revoke reflects.
	if err := log.RevokeDevice(devA, s1); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	if d := log.Devices(); len(d) != 1 || !containsBytes(d, devB) {
		t.Fatalf("after revoke devices = %v", d)
	}
	// Returned slices are copies (mutating them must not corrupt state).
	signers[0][0] ^= 0xFF
	if !log.SignerTrusted(s1.Public) && !log.SignerTrusted(s2.Public) {
		t.Fatal("mutating the returned slice corrupted internal state")
	}
}

func TestSyncStoreEnumeration(t *testing.T) {
	s1, _ := GenerateSigner()
	log, _ := NewGenesis([][]byte{s1.Public}, s1, nil)
	head := log.Head()
	dev := bytes.Repeat([]byte{0x33}, 32)
	_ = log.AuthorizeDevice(dev, s1)
	chain := MarshalChain(log.Entries())

	ss := NewSyncStore(head)
	if _, err := ss.Ingest(chain); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(ss.Signers()) != 1 || !containsBytes(ss.Signers(), s1.Public) {
		t.Fatalf("SyncStore.Signers = %v", ss.Signers())
	}
	if len(ss.Devices()) != 1 || !containsBytes(ss.Devices(), dev) {
		t.Fatalf("SyncStore.Devices = %v", ss.Devices())
	}
}
