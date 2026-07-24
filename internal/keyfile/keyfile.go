// Package keyfile persists a base64-JSON keypair to disk (0600 file under a 0700
// dir), loading an existing one or generating-and-saving a fresh one on first use.
// It centralizes the load/validate/generate/write flow shared by the node's Noise
// identity, trust-log signer, and beacon keys (and the client's Noise identity).
package keyfile

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// persisted is the on-disk form: base64-encoded private and public halves.
type persisted struct {
	Private string `json:"private"`
	Public  string `json:"public"`
}

// LoadOrCreate returns the keypair persisted at path, generating and saving one on
// first use. gen mints a fresh keypair; split extracts its raw (private, public)
// bytes for persistence; build reconstructs the caller's key type from decoded bytes,
// returning ok=false when they fail validation (wrong length, etc.) so a corrupt or
// mismatched file is transparently regenerated. name prefixes read errors.
func LoadOrCreate[T any](
	path, name string,
	gen func() (T, error),
	split func(T) (priv, pub []byte),
	build func(priv, pub []byte) (T, bool),
) (T, error) {
	var zero T
	if b, err := os.ReadFile(path); err == nil {
		var p persisted
		if json.Unmarshal(b, &p) == nil {
			priv, e1 := base64.StdEncoding.DecodeString(p.Private)
			pub, e2 := base64.StdEncoding.DecodeString(p.Public)
			if e1 == nil && e2 == nil {
				if v, ok := build(priv, pub); ok {
					return v, nil
				}
			}
		}
	} else if !os.IsNotExist(err) {
		return zero, fmt.Errorf("%s: reading key %s: %w", name, path, err)
	}

	v, err := gen()
	if err != nil {
		return zero, err
	}
	priv, pub := split(v)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return zero, err
	}
	b, err := json.Marshal(persisted{
		Private: base64.StdEncoding.EncodeToString(priv),
		Public:  base64.StdEncoding.EncodeToString(pub),
	})
	if err != nil {
		return zero, err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return zero, err
	}
	return v, nil
}
