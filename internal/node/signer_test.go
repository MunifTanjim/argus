package node

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateSignerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "signer-key.json")

	kp1, err := LoadOrCreateSigner(path)
	if err != nil {
		t.Fatalf("first LoadOrCreateSigner: %v", err)
	}
	if len(kp1.Public) != ed25519.PublicKeySize || len(kp1.Private) != ed25519.PrivateKeySize {
		t.Fatalf("bad key sizes: pub=%d priv=%d", len(kp1.Public), len(kp1.Private))
	}
	// File is 0600.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perms = %v, want 0600", fi.Mode().Perm())
	}
	// Second call returns the identical persisted key.
	kp2, err := LoadOrCreateSigner(path)
	if err != nil {
		t.Fatalf("second LoadOrCreateSigner: %v", err)
	}
	if !kp1.Public.Equal(kp2.Public) || string(kp1.Private) != string(kp2.Private) {
		t.Fatal("second load returned a different key")
	}
}

func TestLoadOrCreateSignerRegeneratesOnCorrupt(t *testing.T) {
	t.Run("not json", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "signer-key.json")
		if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		kp, err := LoadOrCreateSigner(path)
		if err != nil {
			t.Fatalf("LoadOrCreateSigner: %v", err)
		}
		if len(kp.Public) != ed25519.PublicKeySize {
			t.Fatal("expected a freshly generated key")
		}
	})

	t.Run("wrong key sizes", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "signer-key.json")
		// 32-byte private is invalid for Ed25519 (expects 64 bytes); 32-byte public is
		// correct size but paired with a wrong private — loader must reject it.
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
		kp, err := LoadOrCreateSigner(path)
		if err != nil {
			t.Fatalf("LoadOrCreateSigner: %v", err)
		}
		if len(kp.Private) != ed25519.PrivateKeySize || len(kp.Public) != ed25519.PublicKeySize {
			t.Fatalf("expected freshly generated key: priv=%d pub=%d", len(kp.Private), len(kp.Public))
		}
	})
}

func TestLoadOrCreateSignerUnreadable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	path := filepath.Join(t.TempDir(), "signer-key.json")
	if err := os.WriteFile(path, []byte("{}"), 0o000); err != nil {
		t.Fatalf("write: %v", err)
	}
	defer os.Chmod(path, 0o600)
	_, err := LoadOrCreateSigner(path)
	if err == nil {
		t.Fatal("expected an error for an unreadable key file, got nil")
	}
}

func TestSetSignerKeyExposesPub(t *testing.T) {
	d := New()
	if d.SignerPubKey() != "" {
		t.Fatal("unset signer pubkey should be empty")
	}
	kp, err := LoadOrCreateSigner(filepath.Join(t.TempDir(), "signer-key.json"))
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	d.SetSignerKey(kp)
	if got := d.SignerPubKey(); got != base64.StdEncoding.EncodeToString(kp.Public) {
		t.Fatalf("SignerPubKey = %q, want base64 of the public half", got)
	}
}
