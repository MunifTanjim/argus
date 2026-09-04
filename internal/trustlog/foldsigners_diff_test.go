package trustlog

import (
	"reflect"
	"testing"
)

// foldSignersViaApply is the OLD reference implementation: replay the prefix
// through apply (full verification) and return the signer set. The optimized
// foldSignersAt must match it exactly.
func foldSignersViaApply(entries []Entry, p int) (map[string]bool, error) {
	l := newEmpty()
	for i := 0; i < p; i++ {
		if err := l.apply(&entries[i]); err != nil {
			return nil, err
		}
	}
	return l.signers, nil
}

func TestFoldSignersAtMatchesApply(t *testing.T) {
	for seed := int64(1); seed <= 60; seed++ {
		g := genChain(t, seed, 10)
		for p := 1; p <= len(g.entries); p++ {
			want, err := foldSignersViaApply(g.entries, p)
			if err != nil {
				t.Fatalf("seed %d p %d: reference fold: %v", seed, p, err)
			}
			got, err := foldSignersAt(g.entries, p)
			if err != nil {
				t.Fatalf("seed %d p %d: foldSignersAt: %v", seed, p, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("seed %d p %d: fold signer set mismatch\n got=%v\nwant=%v", seed, p, got, want)
			}
		}
	}
}
