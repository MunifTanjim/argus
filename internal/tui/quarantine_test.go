package tui

import (
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

type stubQuarantinedClient struct{ q bool }

func (c *stubQuarantinedClient) Call(_ string, _, _ any) error       { return nil }
func (c *stubQuarantinedClient) Events() <-chan api.Notification      { return nil }
func (c *stubQuarantinedClient) States() <-chan bool                  { return nil }
func (c *stubQuarantinedClient) Reconnect()                          {}
func (c *stubQuarantinedClient) Close() error                        { return nil }
func (c *stubQuarantinedClient) Quarantined() bool                   { return c.q }

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
