package spawn

import (
	"context"
	"os/exec"
	"testing"

	"github.com/MunifTanjim/argus/internal/tmux"
)

func TestDefaultSessionName(t *testing.T) {
	cases := map[string]string{
		"/Users/m/Dev/github/MunifTanjim/argus": "argus",
		"/Users/m/Dev/work/cmp/":                "cmp",
		"":                                      "claude",
		"/":                                     "claude",
		".":                                     "claude",
		" ":                                     "claude",
	}
	for in, want := range cases {
		if got := defaultSessionName(in); got != want {
			t.Errorf("defaultSessionName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUniqueName(t *testing.T) {
	taken := map[string]bool{"argus": true, "argus-2": true}
	if got := uniqueName("argus", taken); got != "argus-3" {
		t.Errorf("uniqueName collision = %q, want %q", got, "argus-3")
	}
	if got := uniqueName("cmp", taken); got != "cmp" {
		t.Errorf("uniqueName free = %q, want %q", got, "cmp")
	}
}

// SessionName de-dupes against the names already present on the server.
func TestSessionName(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	ctx := context.Background()
	c := tmux.New("argus-spawn-name-test")
	t.Cleanup(func() { _ = c.KillServer(ctx) })

	dir := t.TempDir() // basename is a random temp name; reuse it as the base
	first := SessionName(ctx, c, dir)
	if _, err := c.NewSession(ctx, tmux.NewSessionOpts{Name: first, Cwd: dir}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// With the first name taken, the next call must pick a distinct name.
	if second := SessionName(ctx, c, dir); second == first {
		t.Fatalf("SessionName returned %q twice; want a deduped name", second)
	}
}
