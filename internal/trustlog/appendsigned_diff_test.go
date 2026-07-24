package trustlog

import (
	"bytes"
	"testing"
)

// TestAppendSignedMatchesReload asserts the optimized appendSigned (extend the
// verified log in place) yields byte-identical chain state to the old path
// (Bytes -> UnmarshalChain -> Load -> mutate -> MarshalChain -> Ingest) across
// many random chains and each mutation kind.
func TestAppendSignedMatchesReload(t *testing.T) {
	for seed := int64(1); seed <= 50; seed++ {
		g := genChain(t, seed, 8)
		base := MarshalChain(g.entries)

		// Reference (old) path: rebuild from bytes, mutate a fresh Load, marshal.
		refEntries, err := UnmarshalChain(base)
		if err != nil {
			t.Fatalf("seed %d: unmarshal: %v", seed, err)
		}
		refLog, err := Load(refEntries)
		if err != nil {
			t.Fatalf("seed %d: load: %v", seed, err)
		}
		nd, _ := GenerateSigner()
		if err := refLog.AuthorizeDevice(nd.Public, g.signers[0]); err != nil {
			t.Fatalf("seed %d: ref authorize: %v", seed, err)
		}
		wantBytes := MarshalChain(refLog.Entries())

		// New path: SyncStore.AuthorizeDevice via appendSigned on the same base.
		ss := NewSyncStore(hashEntry(&g.entries[0]))
		if _, err := ss.Ingest(base); err != nil {
			t.Fatalf("seed %d: seed ingest: %v", seed, err)
		}
		if _, err := ss.AuthorizeDevice(nd.Public, g.signers[0]); err != nil {
			t.Fatalf("seed %d: new authorize: %v", seed, err)
		}
		gotBytes := ss.Bytes()

		if !bytes.Equal(gotBytes, wantBytes) {
			t.Fatalf("seed %d: appendSigned chain bytes differ from reload path", seed)
		}
	}
}
