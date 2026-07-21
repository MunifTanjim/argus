package node

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/trustlog"
)

// persistedSigner is the on-disk form of the node's Ed25519 signer keypair.
type persistedSigner struct {
	Private string `json:"private"` // base64 Ed25519 private (64 bytes)
	Public  string `json:"public"`  // base64 Ed25519 public (32 bytes)
}

// LoadOrCreateSigner loads the node's persisted Ed25519 signer keypair, generating
// and saving one on first use (0600 file under a 0700 dir), mirroring the Noise
// identity. The signer key is minted unconditionally at bootstrap — even in open
// mode — so a later `lock init` can designate an already-existing key as a trusted
// signer with no re-provisioning. The private half never leaves the node.
func LoadOrCreateSigner(path string) (trustlog.SignerKey, error) {
	if b, err := os.ReadFile(path); err == nil {
		var p persistedSigner
		if json.Unmarshal(b, &p) == nil {
			priv, e1 := base64.StdEncoding.DecodeString(p.Private)
			pub, e2 := base64.StdEncoding.DecodeString(p.Public)
			if e1 == nil && e2 == nil && len(priv) == ed25519.PrivateKeySize && len(pub) == ed25519.PublicKeySize {
				return trustlog.SignerKey{Public: ed25519.PublicKey(pub), Private: ed25519.PrivateKey(priv)}, nil
			}
		}
	}
	kp, err := trustlog.GenerateSigner()
	if err != nil {
		return trustlog.SignerKey{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return trustlog.SignerKey{}, err
	}
	b, err := json.Marshal(persistedSigner{
		Private: base64.StdEncoding.EncodeToString(kp.Private),
		Public:  base64.StdEncoding.EncodeToString(kp.Public),
	})
	if err != nil {
		return trustlog.SignerKey{}, err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return trustlog.SignerKey{}, err
	}
	return kp, nil
}
