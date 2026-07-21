package trustlog

import (
	"crypto/rand"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// disablementSecretLen is the size of a disablement secret (high-entropy CSPRNG bytes).
const disablementSecretLen = 32

// Fixed Argon2id parameters for the disablement commitment. The secret is 32 random
// bytes, so the KDF is cryptographic overkill (a plain hash would suffice) — we mirror
// Tailscale's Argon2id DisablementKDF for fidelity. Deterministic: a fixed salt so the
// commitment reproduces for verification. These params are argus's own; commitments
// live only in argus genesis blocks and never cross-validate against Tailscale.
const (
	disablementSalt           = "argus-trustlog-disablement-v1"
	disablementTime    uint32 = 1
	disablementMemory  uint32 = 64 * 1024 // 64 MiB
	disablementThreads uint8  = 4
	disablementKeyLen  uint32 = 32
)

// GenerateDisablementSecret returns a fresh 32-byte disablement secret from the CSPRNG.
func GenerateDisablementSecret() ([]byte, error) {
	b := make([]byte, disablementSecretLen)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("trustlog: generate disablement secret: %w", err)
	}
	return b, nil
}

// DisablementCommitment is the one-way Argon2id commitment for a disablement secret,
// stored (tamper-evidently) in the genesis. Deterministic. Recomputed during Load/Ingest
// whenever a KindDisable entry is encountered to verify the revealed secret. Because a
// disabled log is terminal (no further entries are accepted after the first valid
// KindDisable), this runs at most once per Load.
func DisablementCommitment(secret []byte) []byte {
	return argon2.IDKey(secret, []byte(disablementSalt), disablementTime, disablementMemory, disablementThreads, disablementKeyLen)
}
