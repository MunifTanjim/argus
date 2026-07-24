package api

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
)

// Beacon is a signed HEAD announcement emitted by a node. BeaconPub is the
// node's Ed25519 beacon public key (32 bytes); Tip is the current trust-log tip
// hash; Length is the number of entries in the chain; Counter is a monotonic
// per-node emission counter (bumped on each emission; a beacon with a counter ≤
// the last seen for that node is stale and ignored). Sig is an Ed25519 signature
// over the deterministic encoding of all other fields.
type Beacon struct {
	BeaconPub []byte `json:"beacon_pub"`
	Tip       []byte `json:"tip,omitempty"`
	Length    int    `json:"length"`
	Counter   uint64 `json:"counter"`
	Sig       []byte `json:"sig,omitempty"`
}

// beaconPutField appends a 4-byte big-endian length prefix and b to buf,
// matching the trustlog codec's putField convention.
func beaconPutField(buf *bytes.Buffer, b []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(b)))
	buf.Write(n[:])
	buf.Write(b)
}

// beaconSigBytes returns the deterministic byte string a beacon signature
// covers: beaconPub and tip are length-prefixed (4-byte BE count + bytes);
// length and counter are 8-byte big-endian scalars.
func beaconSigBytes(beaconPub, tip []byte, length int, counter uint64) []byte {
	var buf bytes.Buffer
	beaconPutField(&buf, beaconPub)
	beaconPutField(&buf, tip)
	var l [8]byte
	binary.BigEndian.PutUint64(l[:], uint64(length))
	buf.Write(l[:])
	var c [8]byte
	binary.BigEndian.PutUint64(c[:], counter)
	buf.Write(c[:])
	return buf.Bytes()
}

// SignBeacon signs a beacon for the given fields using the Ed25519 private key.
// The BeaconPub field in the returned Beacon is set to the raw public-key bytes
// the caller supplies (typically the beacon keypair's public half).
func SignBeacon(key ed25519.PrivateKey, beaconPub, tip []byte, length int, counter uint64) Beacon {
	msg := beaconSigBytes(beaconPub, tip, length, counter)
	return Beacon{
		BeaconPub: beaconPub,
		Tip:       tip,
		Length:    length,
		Counter:   counter,
		Sig:       ed25519.Sign(key, msg),
	}
}

// VerifyBeacon checks that b.Sig is a valid Ed25519 signature over the
// deterministic encoding of b's fields, using b.BeaconPub as the verifying key.
// Returns false if BeaconPub is not exactly 32 bytes, if Sig is absent, or if
// the signature does not verify.
func VerifyBeacon(b Beacon) bool {
	if len(b.BeaconPub) != ed25519.PublicKeySize {
		return false
	}
	if len(b.Sig) == 0 {
		return false
	}
	msg := beaconSigBytes(b.BeaconPub, b.Tip, b.Length, b.Counter)
	return ed25519.Verify(ed25519.PublicKey(b.BeaconPub), msg, b.Sig)
}
