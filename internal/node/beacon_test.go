package node

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// TestBeaconCounterPersistsAcrossRestart verifies that the beacon counter is
// written to a sibling file and seeded from it on the next node startup, so
// peers never see the counter reset to 1 and drop fresh beacons as stale.
func TestBeaconCounterPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "beacon-key.json")

	kp, err := LoadOrCreateBeaconKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateBeaconKey: %v", err)
	}

	// "First session": create node, set beacon key+path, make beacons.
	d1 := newNode(nil)
	d1.SetBeaconKey(kp)
	d1.SetBeaconCounterPath(keyPath) // no file yet; seeds from 0

	b1 := d1.makeBeacon() // counter=1
	b2 := d1.makeBeacon() // counter=2
	b3 := d1.makeBeacon() // counter=3
	_ = b1
	_ = b2
	// Persist the current counter value (emitBeacon does this in production).
	if err := persistBeaconCounter(keyPath, b3.Counter); err != nil {
		t.Fatalf("persistBeaconCounter: %v", err)
	}

	// Verify the counter file is readable.
	loaded := LoadBeaconCounter(keyPath)
	if loaded != 3 {
		t.Fatalf("LoadBeaconCounter = %d, want 3", loaded)
	}

	// "Second session" (restart): new node, same key path — must seed from file.
	d2 := newNode(nil)
	d2.SetBeaconKey(kp)
	d2.SetBeaconCounterPath(keyPath) // reads file → seeds counter at 3

	b4 := d2.makeBeacon() // must be > b3.Counter
	if b4.Counter <= b3.Counter {
		t.Fatalf("post-restart counter = %d, must be > %d (last pre-restart counter)",
			b4.Counter, b3.Counter)
	}
}

// TestLoadBeaconCounterMissingFile verifies that LoadBeaconCounter returns 0
// when the sibling counter file does not exist yet.
func TestLoadBeaconCounterMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "beacon-key.json")
	if got := LoadBeaconCounter(path); got != 0 {
		t.Fatalf("LoadBeaconCounter missing file = %d, want 0", got)
	}
}

// TestLoadBeaconCounterCorruptFile verifies that LoadBeaconCounter returns 0
// gracefully when the counter file contains non-numeric garbage (e.g. a prior
// crash left a partial write). With atomic writes the scenario is hypothetical,
// but the degradation path must remain robust.
func TestLoadBeaconCounterCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "beacon-key.json")
	if err := os.WriteFile(path+".counter", []byte("not-a-number"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := LoadBeaconCounter(path); got != 0 {
		t.Fatalf("LoadBeaconCounter corrupt file = %d, want 0", got)
	}
}

// TestPersistBeaconCounterAtomicSurvival verifies that persistBeaconCounter writes
// the counter file so that LoadBeaconCounter reads back the exact value, and that
// the file is stable (no torn-write edge case, since atomicfile uses temp+rename).
func TestPersistBeaconCounterAtomicSurvival(t *testing.T) {
	path := filepath.Join(t.TempDir(), "beacon-key.json")
	const want uint64 = 42
	if err := persistBeaconCounter(path, want); err != nil {
		t.Fatalf("persistBeaconCounter: %v", err)
	}
	if got := LoadBeaconCounter(path); got != want {
		t.Fatalf("LoadBeaconCounter after persist = %d, want %d", got, want)
	}
}

func TestLoadOrCreateBeaconKeyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "beacon-key.json")

	kp1, err := LoadOrCreateBeaconKey(path)
	if err != nil {
		t.Fatalf("first LoadOrCreateBeaconKey: %v", err)
	}
	if len(kp1.Public) != ed25519.PublicKeySize || len(kp1.Private) != ed25519.PrivateKeySize {
		t.Fatalf("bad key sizes: pub=%d priv=%d", len(kp1.Public), len(kp1.Private))
	}
	// File must be 0600.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perms = %v, want 0600", fi.Mode().Perm())
	}
	// Second call must return the identical persisted key.
	kp2, err := LoadOrCreateBeaconKey(path)
	if err != nil {
		t.Fatalf("second LoadOrCreateBeaconKey: %v", err)
	}
	if !kp1.Public.Equal(kp2.Public) || string(kp1.Private) != string(kp2.Private) {
		t.Fatal("second load returned a different key")
	}
}

func TestLoadOrCreateBeaconKeyRegeneratesOnCorrupt(t *testing.T) {
	t.Run("not json", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "beacon-key.json")
		if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		kp, err := LoadOrCreateBeaconKey(path)
		if err != nil {
			t.Fatalf("LoadOrCreateBeaconKey: %v", err)
		}
		if len(kp.Public) != ed25519.PublicKeySize {
			t.Fatal("expected a freshly generated key")
		}
	})

	t.Run("wrong key sizes", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "beacon-key.json")
		// 32-byte private is invalid for Ed25519 (expects 64 bytes); loader must reject it.
		bad := struct {
			Private string `json:"private"`
			Public  string `json:"public"`
		}{
			Private: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, 32)),
			Public:  base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x02}, 32)),
		}
		data, err := json.Marshal(bad)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		kp, err := LoadOrCreateBeaconKey(path)
		if err != nil {
			t.Fatalf("LoadOrCreateBeaconKey: %v", err)
		}
		if len(kp.Private) != ed25519.PrivateKeySize || len(kp.Public) != ed25519.PublicKeySize {
			t.Fatalf("expected freshly generated key: priv=%d pub=%d", len(kp.Private), len(kp.Public))
		}
	})
}

func TestLoadOrCreateBeaconKeyUnreadable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	path := filepath.Join(t.TempDir(), "beacon-key.json")
	if err := os.WriteFile(path, []byte("{}"), 0o000); err != nil {
		t.Fatalf("write: %v", err)
	}
	defer os.Chmod(path, 0o600)
	_, err := LoadOrCreateBeaconKey(path)
	if err == nil {
		t.Fatal("expected an error for an unreadable key file, got nil")
	}
}

func TestSetBeaconKeyExposesPub(t *testing.T) {
	d := New()
	if d.BeaconPub() != "" {
		t.Fatal("unset beacon pubkey should be empty")
	}
	kp, err := LoadOrCreateBeaconKey(filepath.Join(t.TempDir(), "beacon-key.json"))
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	d.SetBeaconKey(kp)
	if got := d.BeaconPub(); got != base64.StdEncoding.EncodeToString(kp.Public) {
		t.Fatalf("BeaconPub = %q, want base64 of the public half", got)
	}
}

// TestMakeBeaconCounterAndState verifies makeBeacon bumps the counter on each
// call, embeds the correct BeaconPub, and reflects the trust store's tip+length.
func TestMakeBeaconCounterAndState(t *testing.T) {
	d := New()
	kp, err := LoadOrCreateBeaconKey(filepath.Join(t.TempDir(), "beacon-key.json"))
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	d.SetBeaconKey(kp)

	// Without trust store: tip=nil, length=0, counter increments from 1.
	b1 := d.makeBeacon()
	if b1.Counter != 1 {
		t.Fatalf("first beacon counter = %d, want 1", b1.Counter)
	}
	if b1.Tip != nil || b1.Length != 0 {
		t.Fatalf("no trust: want tip=nil length=0, got tip=%x len=%d", b1.Tip, b1.Length)
	}
	if !bytes.Equal(b1.BeaconPub, kp.Public) {
		t.Fatal("BeaconPub does not match the beacon keypair's public half")
	}
	if !api.VerifyBeacon(b1) {
		t.Fatal("first beacon signature invalid")
	}

	// Second call bumps counter.
	b2 := d.makeBeacon()
	if b2.Counter != 2 {
		t.Fatalf("second beacon counter = %d, want 2", b2.Counter)
	}
	if !api.VerifyBeacon(b2) {
		t.Fatal("second beacon signature invalid")
	}

	// With trust store: tip and length are reflected.
	signer, serr := trustlog.GenerateSigner()
	if serr != nil {
		t.Fatalf("GenerateSigner: %v", serr)
	}
	// Build a genesis-only log to pin the genesis hash.
	genLog, gerr := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if gerr != nil {
		t.Fatalf("NewGenesis: %v", gerr)
	}
	genesisHash := genLog.Tip() // tip after genesis = hash of the genesis entry

	// Authorize a device to get a 2-entry chain (genesis + authorize).
	device := bytes.Repeat([]byte{0x42}, 32)
	if aerr := genLog.AuthorizeDevice(device, signer); aerr != nil {
		t.Fatalf("AuthorizeDevice: %v", aerr)
	}
	chain := trustlog.MarshalChain(genLog.Entries())

	ss := trustlog.NewSyncStore(genesisHash)
	if _, ierr := ss.Ingest(chain); ierr != nil {
		t.Fatalf("Ingest: %v", ierr)
	}
	d.trust.Store(ss)

	b3 := d.makeBeacon()
	if b3.Counter != 3 {
		t.Fatalf("third beacon counter = %d, want 3", b3.Counter)
	}
	if !bytes.Equal(b3.Tip, ss.Tip()) {
		t.Fatalf("beacon Tip = %x, want %x", b3.Tip, ss.Tip())
	}
	if b3.Length != ss.Length() {
		t.Fatalf("beacon Length = %d, want %d", b3.Length, ss.Length())
	}
	if b3.Length != 2 {
		t.Fatalf("expected 2-entry chain (genesis+authorize), got Length=%d", b3.Length)
	}
	if !api.VerifyBeacon(b3) {
		t.Fatal("third beacon (with trust) signature invalid")
	}
}
