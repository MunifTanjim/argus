package main

import (
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/keyfmt"
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
	if strings.Contains(out, "locked mode: disabled network-wide") {
		t.Fatal("the break-glass headline is subsumed by the supersession one")
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

// A superseded device's own log being disabled is background: what matters is that
// THIS device is refusing everything. Leading with "nothing is enforced" reads as a
// contradiction two lines above "this device refuses all channels".
func TestLockStatusLinesLeadsWithQuarantineWhenSuperseded(t *testing.T) {
	out, _ := lockStatusLines(api.LockStatusResult{
		Enabled:     true,
		Disabled:    true,
		Pinned:      true,
		Quarantined: true,
		PinGenesis:  testGenesis(0xC1),
		SeenGenesis: testGenesis(0xC2),
	})
	head := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(head, "QUARANTINED") {
		t.Fatalf("headline %q must lead with the device's own state", head)
	}
	// The break-glass headline (lockStatusLines' st.Disabled branch) says "nothing is
	// enforced", which reads as the opposite of refusing every channel. A superseded
	// device must not fall through to it: it is the most restrictive state there is.
	if strings.Contains(out, "nothing is enforced") {
		t.Fatalf("output %q must not claim it enforces nothing while refusing every channel", out)
	}
	if n := strings.Count(out, "refuses all channels"); n != 1 {
		t.Fatalf("output %q states the refusal %d times, want exactly 1", out, n)
	}
}

// After two relocks the gateway offers several roots, and bare `lock pin` refuses to
// choose between them — so the instruction has to carry the genesis.
func TestLockStatusLinesGivesAnExplicitPinCommand(t *testing.T) {
	seen := testGenesis(0xC2)
	for _, st := range []api.LockStatusResult{
		{Enabled: true, Disabled: true, Pinned: true, Quarantined: true, PinGenesis: testGenesis(0xC1), SeenGenesis: seen},
		{Quarantined: true, SeenGenesis: seen},
	} {
		out, _ := lockStatusLines(st)
		if want := "argus lock pin " + keyfmt.Genesis.Encode(seen); !strings.Contains(out, want) {
			t.Fatalf("output %q must contain %q", out, want)
		}
	}
}
