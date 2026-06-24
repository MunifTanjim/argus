package tui

import (
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

func nodeSession(id, nodeID, label string, status session.Status) session.Session {
	return session.Session{
		ID: id, NodeID: nodeID, NodeLabel: label, Status: status,
		Tmux: session.TmuxLocation{PaneID: "%1"},
	}
}

func modelWith(sessions ...session.Session) model {
	m := testModel()
	m.sessions = map[string]session.Session{}
	for _, s := range sessions {
		m.sessions[s.ID] = s
	}
	m.reorder()
	return m
}

func indexOf(t *testing.T, hay, needle string) int {
	t.Helper()
	i := strings.Index(hay, needle)
	if i < 0 {
		t.Fatalf("expected to find %q in:\n%s", needle, hay)
	}
	return i
}

func TestListViewGroupsByHost(t *testing.T) {
	m := modelWith(
		nodeSession("beta:%1", "beta", "beta", session.StatusIdle),
		nodeSession("alpha:%1", "alpha", "alpha", session.StatusIdle),
	)
	out := m.listView()

	// Both host headers present, alpha before beta (alphabetical, none awaiting).
	a := indexOf(t, out, "▌ alpha")
	b := indexOf(t, out, "▌ beta")
	if a >= b {
		t.Fatalf("alpha group should come before beta:\n%s", out)
	}
}

func TestListViewNoHeadersWhenLocal(t *testing.T) {
	m := modelWith(
		session.Session{ID: "s1", Status: session.StatusIdle, Tmux: session.TmuxLocation{PaneID: "%1"}},
	)
	if out := m.listView(); strings.Contains(out, "▌ ") {
		t.Fatalf("local (no node label) list should have no host headers:\n%s", out)
	}
}

func TestReorderAwaitingHostSortsFirst(t *testing.T) {
	m := modelWith(
		nodeSession("alpha:%1", "alpha", "alpha", session.StatusIdle),
		nodeSession("beta:%1", "beta", "beta", session.StatusAwaitingInput),
	)
	// beta has an awaiting-input session, so its group floats above alpha.
	if m.sessions[m.order[0]].NodeLabel != "beta" {
		t.Fatalf("awaiting host should sort first, got order %v", m.order)
	}
}

func TestReorderAwaitingFloatsWithinHost(t *testing.T) {
	m := modelWith(
		nodeSession("alpha:z", "alpha", "alpha", session.StatusIdle),
		nodeSession("alpha:a", "alpha", "alpha", session.StatusAwaitingInput),
	)
	// Within the same host, awaiting-input floats up regardless of id ordering.
	if m.order[0] != "alpha:a" {
		t.Fatalf("awaiting session should be first within host, got %v", m.order)
	}
}

func TestReorderAwaitingFirstCrossHost(t *testing.T) {
	m := modelWith(
		nodeSession("beta:2", "beta", "beta", session.StatusIdle),
		nodeSession("alpha:1", "alpha", "alpha", session.StatusWorking),
		nodeSession("beta:1", "beta", "beta", session.StatusAwaitingInput),
		nodeSession("alpha:2", "alpha", "alpha", session.StatusAwaitingInput),
	)
	// Awaiting first (cross-host: alpha before beta, id tiebreak), then the rest by host.
	want := []string{"alpha:2", "beta:1", "alpha:1", "beta:2"}
	for i, id := range want {
		if m.order[i] != id {
			t.Fatalf("order = %v, want %v", m.order, want)
		}
	}
}

func TestListViewNeedsYouSection(t *testing.T) {
	m := modelWith(
		nodeSession("alpha:work", "alpha", "alpha", session.StatusIdle),
		nodeSession("alpha:wait", "alpha", "alpha", session.StatusAwaitingInput),
		nodeSession("beta:wait", "beta", "beta", session.StatusAwaitingInput),
	)
	out := m.listView()
	ny := indexOf(t, out, "Needs you")
	ha := indexOf(t, out, "▌ alpha")
	// "Needs you" precedes the per-host group headers.
	if ny >= ha {
		t.Fatalf("'Needs you' should precede host headers:\n%s", out)
	}
	// The two awaiting sessions are NOT split by a host header between them — there
	// is exactly one host header for alpha (its non-awaiting group), none for beta
	// (beta has only an awaiting session, which lives under Needs you).
	if strings.Count(out, "▌ alpha") != 1 {
		t.Fatalf("expected exactly one alpha host header:\n%s", out)
	}
	if strings.Contains(out, "▌ beta") {
		t.Fatalf("beta has no non-awaiting session, so no beta host header expected:\n%s", out)
	}
}

func TestOfflineGroupAndCard(t *testing.T) {
	off := nodeSession("home:%1", "home", "home", session.StatusWorking)
	off.Offline = true
	off.Repo = "argus"
	m := modelWith(off)
	out := m.listView()

	if !strings.Contains(out, "▌ home") || !strings.Contains(out, "(offline)") {
		t.Fatalf("offline node header should be flagged:\n%s", out)
	}
	if !strings.Contains(out, "(node offline)") {
		t.Fatalf("offline session card should show the offline task line:\n%s", out)
	}
}
