package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

// TestBeaconGoldenVector loads the shared beacon section from the cross-language
// golden vector file and asserts Go's VerifyBeacon gives the expected results,
// pinning Go↔Dart parity. Run GEN_E2E_VECTORS=1 go test ./internal/e2e/ first
// to (re)generate the vector file.
func TestBeaconGoldenVector(t *testing.T) {
	raw, err := os.ReadFile("../../app/test/e2e/testdata/vectors.json")
	if err != nil {
		t.Fatalf("read vectors.json: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("unmarshal vectors.json: %v", err)
	}
	beaconRaw, ok := top["beacon"]
	if !ok {
		t.Fatalf("vectors.json has no 'beacon' section; regenerate with GEN_E2E_VECTORS=1")
	}
	var bv struct {
		Valid struct {
			BeaconPub string `json:"beacon_pub"`
			Tip       string `json:"tip"`
			Length    int    `json:"length"`
			Counter   uint64 `json:"counter"`
			Sig       string `json:"sig"`
		} `json:"valid"`
		Tampered struct {
			BeaconPub string `json:"beacon_pub"`
			Tip       string `json:"tip"`
			Length    int    `json:"length"`
			Counter   uint64 `json:"counter"`
			Sig       string `json:"sig"`
		} `json:"tampered"`
	}
	if err := json.Unmarshal(beaconRaw, &bv); err != nil {
		t.Fatalf("unmarshal beacon: %v", err)
	}
	dec := func(s string) []byte {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			t.Fatalf("base64 decode %q: %v", s, err)
		}
		return b
	}
	valid := Beacon{
		BeaconPub: dec(bv.Valid.BeaconPub),
		Tip:       dec(bv.Valid.Tip),
		Length:    bv.Valid.Length,
		Counter:   bv.Valid.Counter,
		Sig:       dec(bv.Valid.Sig),
	}
	if !VerifyBeacon(valid) {
		t.Error("golden valid beacon must verify")
	}
	tampered := Beacon{
		BeaconPub: dec(bv.Tampered.BeaconPub),
		Tip:       dec(bv.Tampered.Tip),
		Length:    bv.Tampered.Length,
		Counter:   bv.Tampered.Counter,
		Sig:       dec(bv.Tampered.Sig),
	}
	if VerifyBeacon(tampered) {
		t.Error("golden tampered beacon must not verify")
	}
}

func TestBeaconSignVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tip := []byte{0xde, 0xad, 0xbe, 0xef}

	b := SignBeacon(priv, pub, tip, 7, 42)

	if !VerifyBeacon(b) {
		t.Fatal("VerifyBeacon: want true for freshly signed beacon, got false")
	}

	// tamper tip
	{
		tmp := b
		tmp.Tip = append([]byte(nil), tip...)
		tmp.Tip[0] ^= 0xFF
		if VerifyBeacon(tmp) {
			t.Fatal("VerifyBeacon: want false after tampering tip, got true")
		}
	}

	// tamper counter
	{
		tmp := b
		tmp.Counter++
		if VerifyBeacon(tmp) {
			t.Fatal("VerifyBeacon: want false after tampering counter, got true")
		}
	}

	// tamper length
	{
		tmp := b
		tmp.Length++
		if VerifyBeacon(tmp) {
			t.Fatal("VerifyBeacon: want false after tampering length, got true")
		}
	}

	// wrong beacon pubkey (a different key's public half)
	{
		pub2, _, _ := ed25519.GenerateKey(rand.Reader)
		tmp := b
		tmp.BeaconPub = pub2
		if VerifyBeacon(tmp) {
			t.Fatal("VerifyBeacon: want false with wrong pubkey, got true")
		}
	}

	// short pubkey
	{
		tmp := b
		tmp.BeaconPub = b.BeaconPub[:16]
		if VerifyBeacon(tmp) {
			t.Fatal("VerifyBeacon: want false with short pubkey, got true")
		}
	}

	// absent sig
	{
		tmp := b
		tmp.Sig = nil
		if VerifyBeacon(tmp) {
			t.Fatal("VerifyBeacon: want false with nil sig, got true")
		}
	}
}
