package tui

import (
	"slices"
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

func act(ts string) *session.Summary { return &session.Summary{LastActivity: ts} }

func TestSidebarSessionGroups(t *testing.T) {
	ss := map[string]session.Session{
		"idle-old": {ID: "idle-old", Status: session.StatusIdle, Summary: act("2026-10-11T08:00:00Z")},
		"idle-new": {ID: "idle-new", Status: session.StatusIdle, Summary: act("2026-10-11T09:00:00+02:00")},
		"disc":     {ID: "disc", Status: session.StatusDiscovered},
		"work":     {ID: "work", Status: session.StatusWorking, Summary: act("2026-10-11T07:00:00Z")},
		"start":    {ID: "start", Status: session.StatusStarting, Summary: act("2026-10-11T07:30:00Z")},
		"wait":     {ID: "wait", Status: session.StatusAwaitingInput},
		"dead":     {ID: "dead", Status: session.StatusDead, Summary: act("2026-10-11T10:00:00Z")},
		"off-wait": {ID: "off-wait", Status: session.StatusAwaitingInput, Offline: true},
		"off-dead": {ID: "off-dead", Status: session.StatusDead, Offline: true},
		"bad-time": {ID: "bad-time", Status: session.StatusIdle, Summary: act("yesterday")},
	}
	got := sidebarSessionGroups(ss)
	want := []sidebarSessionGroup{
		{sideNeedsYou, []string{"wait"}},
		{sideWorking, []string{"start", "work"}},
		// 09:00+02:00 is 07:00Z, older than 08:00Z; no or bad activity sorts last, by id.
		{sideIdle, []string{"idle-old", "idle-new", "bad-time", "disc"}},
		{sideOffline, []string{"off-wait"}},
	}
	if len(got) != len(want) {
		t.Fatalf("groups = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].section != want[i].section || !slices.Equal(got[i].ids, want[i].ids) {
			t.Errorf("group %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if ids := sidebarSessionIDs(got); !slices.Equal(ids, []string{"wait", "start", "work", "idle-old", "idle-new", "bad-time", "disc", "off-wait"}) {
		t.Errorf("ids = %v", ids)
	}
}

func TestSidebarSessionGroupsHidesEmptySections(t *testing.T) {
	got := sidebarSessionGroups(map[string]session.Session{
		"a": {ID: "a", Status: session.StatusIdle},
	})
	if len(got) != 1 || got[0].section != sideIdle {
		t.Fatalf("groups = %+v, want only Idle", got)
	}
	if got := sidebarSessionGroups(nil); len(got) != 0 {
		t.Fatalf("no sessions: groups = %+v", got)
	}
}
