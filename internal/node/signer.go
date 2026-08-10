package node

import (
	"crypto/ed25519"

	"github.com/MunifTanjim/argus/internal/keyfile"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// LoadOrCreateSigner loads the node's persisted Ed25519 signer keypair, generating
// and saving one on first use (0600 file under a 0700 dir), mirroring the Noise
// identity. The signer key is minted unconditionally at bootstrap — even in open
// mode — so a later `lock init` can designate an already-existing key as a trusted
// signer with no re-provisioning. The private half never leaves the node.
func LoadOrCreateSigner(path string) (trustlog.SignerKey, error) {
	return keyfile.LoadOrCreate(path, "LoadOrCreateSigner",
		trustlog.GenerateSigner,
		func(k trustlog.SignerKey) (priv, pub []byte) { return k.Private, k.Public },
		signerFromBytes,
	)
}

// signerFromBytes reconstructs an Ed25519 SignerKey from decoded bytes, rejecting
// wrong-length halves. Shared by the signer and beacon key files.
func signerFromBytes(priv, pub []byte) (trustlog.SignerKey, bool) {
	if len(priv) != ed25519.PrivateKeySize || len(pub) != ed25519.PublicKeySize {
		return trustlog.SignerKey{}, false
	}
	return trustlog.SignerKey{Public: ed25519.PublicKey(pub), Private: ed25519.PrivateKey(priv)}, true
}
