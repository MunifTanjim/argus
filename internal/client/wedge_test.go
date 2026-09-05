package client

import "testing"

// wedgeWarn must fire exactly once as the consecutive-failure counter crosses the
// threshold, and keep counting after — so a sustained wedge logs one warning, not
// a per-frame flood.
func TestWedgeWarnFiresOnceAtThreshold(t *testing.T) {
	n := 0
	fired := 0
	for i := 0; i < decryptWedgeWarnAfter*2; i++ {
		if wedgeWarn(&n) {
			fired++
		}
	}
	if fired != 1 {
		t.Fatalf("want exactly one warning crossing the threshold, got %d", fired)
	}
	if n != decryptWedgeWarnAfter*2 {
		t.Fatalf("counter must increment each call, got %d", n)
	}
}

// A reset (a successful decrypt sets the counter to 0) must require another full
// run before warning again.
func TestWedgeWarnResetDefersWarning(t *testing.T) {
	n := 0
	for i := 0; i < decryptWedgeWarnAfter-1; i++ {
		if wedgeWarn(&n) {
			t.Fatal("fired before threshold")
		}
	}
	n = 0
	for i := 0; i < decryptWedgeWarnAfter-1; i++ {
		if wedgeWarn(&n) {
			t.Fatal("fired before threshold after reset")
		}
	}
}
