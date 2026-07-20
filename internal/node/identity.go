package node

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/e2e"
)

// persistedIdentity is the on-disk form of the node's Noise static keypair.
type persistedIdentity struct {
	Private string `json:"private"` // base64 Curve25519 private
	Public  string `json:"public"`  // base64 Curve25519 public
}

// LoadOrCreateIdentity loads the node's persisted Noise keypair, generating and
// saving one on first use (0600 file under a 0700 dir), mirroring the VAPID key.
// A stable identity keeps clients' pinned/cached node pubkey valid across restarts.
func LoadOrCreateIdentity(path string) (e2e.KeyPair, error) {
	if b, err := os.ReadFile(path); err == nil {
		var p persistedIdentity
		if json.Unmarshal(b, &p) == nil {
			priv, e1 := base64.StdEncoding.DecodeString(p.Private)
			pub, e2b := base64.StdEncoding.DecodeString(p.Public)
			if e1 == nil && e2b == nil && len(priv) == 32 && len(pub) == 32 {
				return e2e.KeyPair{Private: priv, Public: pub}, nil
			}
		}
	}
	kp, err := e2e.GenerateKeyPair()
	if err != nil {
		return e2e.KeyPair{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return e2e.KeyPair{}, err
	}
	b, err := json.Marshal(persistedIdentity{
		Private: base64.StdEncoding.EncodeToString(kp.Private),
		Public:  base64.StdEncoding.EncodeToString(kp.Public),
	})
	if err != nil {
		return e2e.KeyPair{}, err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return e2e.KeyPair{}, err
	}
	return kp, nil
}
