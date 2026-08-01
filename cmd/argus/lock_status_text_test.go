package main

import (
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestLockStatusLinesNamesTheThreeStates(t *testing.T) {
	cases := []struct {
		name string
		st   api.LockStatusResult
		want string
	}{
		{"never locked", api.LockStatusResult{}, "locked mode: not enabled"},
		{"enforcing", api.LockStatusResult{Enabled: true, Pinned: true}, "locked mode: enforcing"},
		{"break-glass", api.LockStatusResult{Enabled: true, Disabled: true, Pinned: true}, "locked mode: disabled network-wide"},
	}
	for _, tc := range cases {
		out, _ := lockStatusLines(tc.st)
		if !strings.Contains(out, tc.want) {
			t.Fatalf("%s: output %q must contain %q", tc.name, out, tc.want)
		}
	}
}

// The break-glass state is terminal and the way back is a new genesis; say both.
func TestLockStatusLinesExplainsBreakGlass(t *testing.T) {
	out, _ := lockStatusLines(api.LockStatusResult{Enabled: true, Disabled: true, Pinned: true})
	for _, want := range []string{"permanent", "argus lock init"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q must contain %q", out, want)
		}
	}
}

// A superseded device is pinned AND quarantined; the line must name both roots and
// the one command that fixes it.
func TestLockStatusLinesReportsSupersession(t *testing.T) {
	out, _ := lockStatusLines(api.LockStatusResult{
		Enabled:     true,
		Disabled:    true,
		Pinned:      true,
		Quarantined: true,
		PinGenesis:  testGenesis(0xC1),
		SeenGenesis: testGenesis(0xC2),
	})
	for _, want := range []string{"SUPERSEDED", "argus lock pin"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q must contain %q", out, want)
		}
	}
	if strings.Contains(out, "network-wide disabled: true") {
		t.Fatal("the standalone disabled line is subsumed by the headline")
	}
}

func TestLockStatusLinesKeepsUnpinnedQuarantineWording(t *testing.T) {
	out, _ := lockStatusLines(api.LockStatusResult{Quarantined: true, SeenGenesis: testGenesis(0xC2)})
	for _, want := range []string{"QUARANTINED", "argus lock pin"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q must contain %q", out, want)
		}
	}
}
