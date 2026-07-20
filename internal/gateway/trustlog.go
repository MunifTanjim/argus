package gateway

import (
	"sync"

	"github.com/MunifTanjim/argus/internal/trustlog"
)

// trustStore is the gateway's opaque hold of the network's trust-log chain. The
// gateway is untrusted and blind: it verifies nothing and only keeps the chain
// with the most entries it has been offered — a pure liveness heuristic so a node
// reconnecting with a stale (shorter) chain can't clobber a newer one. Every real
// trust decision happens in a genesis-pinned Store on the nodes/clients, which
// reject any rollback/fork/tamper the gateway might serve.
type trustStore struct {
	mu    sync.Mutex
	bytes []byte
	count int
}

// offer adopts chain iff it parses and has strictly more entries than the current
// hold. Byte length is not a valid proxy for chain length (a many-signer genesis
// can outweigh a longer chain), so it counts entries via the DoS-capped decoder.
func (t *trustStore) offer(chain []byte) {
	entries, err := trustlog.UnmarshalChain(chain)
	if err != nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(entries) > t.count {
		t.bytes = append([]byte(nil), chain...)
		t.count = len(entries)
	}
}

// current returns a copy of the held chain (nil if nothing offered yet).
func (t *trustStore) current() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.bytes == nil {
		return nil
	}
	return append([]byte(nil), t.bytes...)
}
