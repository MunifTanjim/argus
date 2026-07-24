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

	"github.com/MunifTanjim/argus/internal/atomicfile"
)

// persisted is the on-disk form: base64-encoded private and public halves.
type persisted struct {
	Private string `json:"private"`
	Public  string `json:"public"`
}

// LoadOrCreate returns the keypair persisted at path, generating and saving one on
// first use. gen mints a fresh keypair; split extracts its raw (private, public)
// bytes for persistence; build reconstructs the caller's key type from decoded bytes,
// returning ok=false when they fail validation (wrong length, etc.). name prefixes errors.
//
// A key file that is ABSENT is created. A file that is PRESENT but unparseable
// (invalid JSON/base64) or invalid (build returns ok=false) returns an error — it
// is NEVER silently regenerated: these are trust-anchor keys (Noise static,
// trust-log signer, beacon), and silently minting a new one would look like a MITM
// to peers or silently change the trusted signer. The write is atomic (temp+rename)
// so a crash mid-write can't leave a torn file that would fail this check next boot.
func LoadOrCreate[T any](
	path, name string,
	gen func() (T, error),
	split func(T) (priv, pub []byte),
	build func(priv, pub []byte) (T, bool),
) (T, error) {
	var zero T
	switch b, err := os.ReadFile(path); {
	case err == nil:
		var p persisted
		if jerr := json.Unmarshal(b, &p); jerr != nil {
			return zero, fmt.Errorf("%s: key %s is corrupt (invalid JSON); refusing to regenerate: %w", name, path, jerr)
		}
		priv, e1 := base64.StdEncoding.DecodeString(p.Private)
		if e1 != nil {
			return zero, fmt.Errorf("%s: key %s is corrupt (private not base64); refusing to regenerate: %w", name, path, e1)
		}
		pub, e2 := base64.StdEncoding.DecodeString(p.Public)
		if e2 != nil {
			return zero, fmt.Errorf("%s: key %s is corrupt (public not base64); refusing to regenerate: %w", name, path, e2)
		}
		v, ok := build(priv, pub)
		if !ok {
			return zero, fmt.Errorf("%s: key %s is corrupt (failed validation); refusing to regenerate", name, path)
		}
		return v, nil
	case !os.IsNotExist(err):
		return zero, fmt.Errorf("%s: reading key %s: %w", name, path, err)
	}

	// File absent: generate and atomically persist a fresh keypair.
	v, err := gen()
	if err != nil {
		return zero, err
	}
	priv, pub := split(v)
	b, err := json.Marshal(persisted{
		Private: base64.StdEncoding.EncodeToString(priv),
		Public:  base64.StdEncoding.EncodeToString(pub),
	})
	if err != nil {
		return zero, err
	}
	if err := atomicfile.Write(path, b); err != nil {
		return zero, err
	}
	return v, nil
}
