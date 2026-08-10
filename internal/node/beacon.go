package node

import (
	"fmt"
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

// makeBeacon builds and signs a fresh HEAD beacon, bumping the monotonic counter
// and persisting it (when beaconCounterPath is set) so a restarted node always
// seeds the counter above any value peers have already accepted — regardless of
// which emission path (uplink offer, sync-tick offer, or identify response)
// produced the beacon. tip and length come from the current trust store (zero
// values when trust is off). The caller must have a non-empty beacon private key
// (d.beacon.Private).
//
// The counter increment and its persist are serialized under beaconEmitMu so the
// persists commit in counter order: without this, two goroutines could hand out
// counters 10 and 11 but have 10's temp+rename land on disk after 11 was already
// emitted, so a restart would reseed below an emitted counter and reuse it
// (manufactured equivocation). If the persist fails an error is returned and the
// beacon is NOT emitted, so no counter is ever advertised without first being
// durably recorded.
func (d *Node) makeBeacon() (api.Beacon, error) {
	d.beaconEmitMu.Lock()
	counter := d.beaconCounter.Add(1)
	if p := d.beaconCounterPath; p != "" {
		if err := persistBeaconCounter(p, counter); err != nil {
			d.beaconEmitMu.Unlock()
			return api.Beacon{}, fmt.Errorf("node: persist beacon counter: %w", err)
		}
	}
	var tip []byte
	var length int
	if st := d.trust.Load(); st != nil {
		tip = st.Tip()
		length = st.Length()
	}
	d.beaconEmitMu.Unlock()
	return api.SignBeacon(d.beacon.Private, d.beacon.Public, tip, length, counter), nil
}

// emitBeacon produces a fresh beacon and offers it to the gateway over the active
// uplink. It is a no-op when the beacon key is absent, no uplink is connected, or
// the counter could not be persisted (makeBeacon returns an error) — never
// emitting a counter that was not first durably recorded.
func (d *Node) emitBeacon() {
	if len(d.beacon.Private) == 0 {
		return
	}
	peer := d.activeUplink.Load()
	if peer == nil {
		return
	}
	b, err := d.makeBeacon()
	if err != nil {
		return
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
