package node

import (
	"os"
	"strconv"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/atomicfile"
	"github.com/MunifTanjim/argus/internal/keyfile"
	"github.com/MunifTanjim/argus/internal/trustlog"
)

// LoadBeaconCounter reads the persisted beacon counter from a sibling file
// (keyPath + ".counter"). Returns 0 when the file is absent or unreadable so
// the caller can seed the counter from whatever baseline it prefers.
func LoadBeaconCounter(keyPath string) uint64 {
	data, err := os.ReadFile(keyPath + ".counter")
	if err != nil {
		return 0
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// persistBeaconCounter writes counter atomically to the sibling file
// (keyPath + ".counter") via temp-rename so a crash mid-write never leaves a
// torn/corrupt file that would reseed the counter to 0 on restart.
// Best-effort: errors are silently ignored by emitBeacon.
func persistBeaconCounter(keyPath string, counter uint64) error {
	return atomicfile.Write(keyPath+".counter", []byte(strconv.FormatUint(counter, 10)))
}

// makeBeacon builds and signs a fresh HEAD beacon, bumping the monotonic counter.
// tip and length come from the current trust store (zero values when trust is off).
// The caller must have a non-empty beacon private key (d.beacon.Private).
func (d *Node) makeBeacon() api.Beacon {
	counter := d.beaconCounter.Add(1)
	var tip []byte
	var length int
	if st := d.trust.Load(); st != nil {
		tip = st.Tip()
		length = st.Length()
	}
	return api.SignBeacon(d.beacon.Private, d.beacon.Public, tip, length, counter)
}

// emitBeacon produces a fresh beacon and offers it to the gateway over the active
// uplink. It is a no-op when the beacon key is absent or no uplink is connected.
// When beaconCounterPath is set, the new counter is persisted so a restarted node
// seeds the counter above any value peers have already accepted.
func (d *Node) emitBeacon() {
	if len(d.beacon.Private) == 0 {
		return
	}
	peer := d.activeUplink.Load()
	if peer == nil {
		return
	}
	b := d.makeBeacon()
	if p := d.beaconCounterPath; p != "" {
		_ = persistBeaconCounter(p, b.Counter)
	}
	_ = peer.Call(api.MethodBeaconOffer, b, nil)
}

// LoadOrCreateBeaconKey loads the node's persisted Ed25519 beacon keypair,
// generating and saving one on first use (0600 file under a 0700 dir). The
// beacon key is separate from the Noise static (identity.go) and the trust-log
// signer (signer.go); every node holds one regardless of locked mode. The
// private half never leaves the node.
func LoadOrCreateBeaconKey(path string) (trustlog.SignerKey, error) {
	return keyfile.LoadOrCreate(path, "LoadOrCreateBeaconKey",
		trustlog.GenerateSigner,
		func(k trustlog.SignerKey) (priv, pub []byte) { return k.Private, k.Public },
		signerFromBytes,
	)
}
