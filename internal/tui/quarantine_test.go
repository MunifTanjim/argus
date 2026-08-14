package tui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

type stubQuarantinedClient struct{ q bool }

func (c *stubQuarantinedClient) Call(_ string, _, _ any) error  { return nil }
func (c *stubQuarantinedClient) Events() <-chan api.Notification { return nil }
func (c *stubQuarantinedClient) States() <-chan bool             { return nil }
func (c *stubQuarantinedClient) Reconnect()                      {}
func (c *stubQuarantinedClient) Close() error                    { return nil }
func (c *stubQuarantinedClient) Quarantined() bool               { return c.q }

func quarantinedModel(q bool, sessions ...session.Session) model {
	m := modelWith(sessions...)
	m.client = &stubQuarantinedClient{q: q}
	return m
}

func TestListViewQuarantineBanner(t *testing.T) {
	s := session.Session{ID: "s1", Status: session.StatusIdle, Tmux: session.TmuxLocation{PaneID: "%1"}}
	out := quarantinedModel(true, s).listView()
	if !strings.Contains(out, "QUARANTINED") {
		t.Fatalf("quarantined list view missing QUARANTINED banner:\n%s", out)
	}
	if !strings.Contains(out, "argus lock pin") {
		t.Fatalf("quarantined list view missing pin hint:\n%s", out)
	}
}

func TestListViewNoBannerWhenNotQuarantined(t *testing.T) {
	s := session.Session{ID: "s1", Status: session.StatusIdle, Tmux: session.TmuxLocation{PaneID: "%1"}}
	out := quarantinedModel(false, s).listView()
	if strings.Contains(out, "QUARANTINED") {
		t.Fatalf("non-quarantined list view must not show QUARANTINED banner:\n%s", out)
	}
}

func TestEmptyListViewQuarantineBanner(t *testing.T) {
	out := quarantinedModel(true).listView()
	if !strings.Contains(out, "QUARANTINED") {
		t.Fatalf("quarantined empty list view missing QUARANTINED banner:\n%s", out)
	}
}

func TestEmptyListViewNoBannerWhenNotQuarantined(t *testing.T) {
	out := quarantinedModel(false).listView()
	if strings.Contains(out, "QUARANTINED") {
		t.Fatalf("non-quarantined empty list view must not show QUARANTINED banner:\n%s", out)
	}
}

// TestEmptyListViewQuarantineHeight verifies that when quarantined the empty-list
// view accounts for the extra banner row, keeping the welcome block from overlapping
// the footer. pinFooter always produces exactly height lines; the test checks that
// the footer key hint lands on the last line (not buried inside the body).
func TestEmptyListViewQuarantineHeight(t *testing.T) {
	const height, width = 16, 60
	m := quarantinedModel(true)
	m.height, m.width = height, width

	out := m.listView()
	lines := strings.Split(out, "\n")

	if len(lines) != height {
		t.Fatalf("output = %d lines, want %d", len(lines), height)
	}

	last := lipgloss.NewStyle().Render(lines[height-1])
	if !strings.Contains(last, "n") {
		t.Fatalf("footer key hint not found on last line: %q", last)
	}

	// The quarantine banner must appear before the last line.
	bannerLine := -1
	for i, l := range lines {
		if strings.Contains(l, "QUARANTINED") {
			bannerLine = i
			break
		}
	}
	if bannerLine < 0 {
		t.Fatal("QUARANTINED not found in output")
	}
	if bannerLine == height-1 {
		t.Fatal("QUARANTINED banner must not be on the last (footer) line")
	}
}
