package node

import "testing"

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

func TestBuildSpawnOpts(t *testing.T) {
	// Prompt becomes the command's argument; command defaults to claude.
	o := buildSpawnOpts("argus", "/p", "", "fix the bug")
	if o.Name != "argus" || o.Cwd != "/p" || o.Command != "claude" {
		t.Fatalf("opts = %#v", o)
	}
	if len(o.Args) != 1 || o.Args[0] != "fix the bug" {
		t.Fatalf("args = %#v, want [\"fix the bug\"]", o.Args)
	}
	// No prompt → no args; explicit command preserved.
	o2 := buildSpawnOpts("x", "/p", "zsh", "")
	if o2.Command != "zsh" || len(o2.Args) != 0 {
		t.Fatalf("opts2 = %#v", o2)
	}
	// Explicit command + prompt: the prompt is appended as that command's arg.
	o3 := buildSpawnOpts("x", "/p", "zsh", "hi")
	if o3.Command != "zsh" || len(o3.Args) != 1 || o3.Args[0] != "hi" {
		t.Fatalf("opts3 = %#v, want command=zsh args=[\"hi\"]", o3)
	}
}
