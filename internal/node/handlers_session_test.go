package node

import (
	"context"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// A node without tmux must advertise no spawn support and reject sessions.spawn
// with an error, even if a client bypasses the UI gating.
func TestSpawnGuardAndIdentifyReportTmux(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{
		session.TmuxServerArgus: tmux.New("argus-guard-test"),
	})

	d.caps.SpawnSession = false // simulate a host without the tmux binary
	if r, _ := d.handleNodeIdentify(context.Background(), nil); r.(api.IdentifyResult).Capabilities.SpawnSession {
		t.Fatal("identify should report spawn_session=false")
	}
	if _, err := d.handleSessionSpawn(context.Background(), nil); err == nil {
		t.Fatal("spawn should be rejected when tmux is unavailable")
	}

	d.caps.SpawnSession = true
	if r, _ := d.handleNodeIdentify(context.Background(), nil); !r.(api.IdentifyResult).Capabilities.SpawnSession {
		t.Fatal("identify should report spawn_session=true")
	}
}

// A plain node answers server.info with its version and just itself: an empty ID
// (addressed implicitly, no routing namespace) carrying its spawn capability, so a
// direct client can show the version and gate the spawn UI.
func TestServerInfoReportsSelf(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{
		session.TmuxServerArgus: tmux.New("argus-list-test"),
	})
	d.label = "boxy"
	d.version = "9.9"
	d.caps.SpawnSession = false

	r, _ := d.handleServerInfo(context.Background(), nil)
	info := r.(api.ServerInfo)
	if info.Version != "9.9" {
		t.Fatalf("version = %q, want 9.9", info.Version)
	}
	if len(info.Nodes) != 1 {
		t.Fatalf("nodes = %d entries, want 1", len(info.Nodes))
	}
	n := info.Nodes[0]
	if n.ID != "" || n.Label != "boxy" || n.Version != "9.9" || n.Capabilities.SpawnSession {
		t.Fatalf("self entry = %+v", n)
	}
}

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
