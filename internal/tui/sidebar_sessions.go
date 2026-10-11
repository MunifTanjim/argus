package tui

import (
	"slices"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/session"
)

// sidebarSection is a section of the left sidebar's session list, in display
// order.
type sidebarSection int

const (
	sideNeedsYou sidebarSection = iota
	sideWorking
	sideIdle
	sideOffline
)

var sidebarSectionTitles = []string{"Needs you", "Working", "Idle", "Offline"}

// sidebarSectionOf places a session; dead sessions are not listed.
func sidebarSectionOf(s session.Session) (sidebarSection, bool) {
	switch {
	case s.Status == session.StatusDead:
		return 0, false
	case s.Offline:
		return sideOffline, true
	case s.Status == session.StatusAwaitingInput:
		return sideNeedsYou, true
	case s.Status == session.StatusWorking, s.Status == session.StatusStarting:
		return sideWorking, true
	}
	return sideIdle, true
}

type sidebarSessionGroup struct {
	section sidebarSection
	ids     []string
}

// sidebarSessionGroups lists the sessions by section, the most recently active
// first in each; empty sections are left out.
func sidebarSessionGroups(sessions map[string]session.Session) []sidebarSessionGroup {
	by := map[sidebarSection][]string{}
	for id, s := range sessions {
		if sec, ok := sidebarSectionOf(s); ok {
			by[sec] = append(by[sec], id)
		}
	}
	var out []sidebarSessionGroup
	for i := range sidebarSectionTitles {
		sec := sidebarSection(i)
		ids := by[sec]
		if len(ids) == 0 {
			continue
		}
		slices.SortFunc(ids, func(a, b string) int { return byActivity(sessions[a], sessions[b]) })
		out = append(out, sidebarSessionGroup{sec, ids})
	}
	return out
}

func sidebarSessionIDs(groups []sidebarSessionGroup) []string {
	var ids []string
	for _, g := range groups {
		ids = append(ids, g.ids...)
	}
	return ids
}

// byActivity puts the newest activity first; sessions without one go last.
func byActivity(a, b session.Session) int {
	if c := lastActivity(b).Compare(lastActivity(a)); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

func lastActivity(s session.Session) time.Time {
	if s.Summary == nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, s.Summary.LastActivity)
	return t
}
