package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestParseOpencodeProcs(t *testing.T) {
	ps := strings.Join([]string{
		"111 ttys001 Ss /opt/homebrew/bin/opencode --session ses_a",
		"222 ttys002 Ss opencode --session=ses_b",
		"333 ?? Ss opencode --session ses_c", // ttyless: skipped
		"444 ttys003 Ss vim --session ses_d", // not opencode: skipped
		"555 ttys004 Ss opencode -s ses_e",
	}, "\n")
	got := parseOpencodeProcs(ps)
	want := map[string]string{"ses_a": "ttys001", "ses_b": "ttys002", "ses_e": "ttys004"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: got %q want %q (%v)", k, got[k], v, got)
		}
	}
}

func TestReconcilePanesAdoptsCreatesAndDetaches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/session/") {
			_, _ = w.Write([]byte(`{"data":{"id":"x","title":"T","location":{"directory":"/repo"}}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	reg := registry.New()
	d := newTestDiscoverer(reg, newClient(serviceInfo{URL: srv.URL}))

	// ses_known already tracked (paneless idle); ses_new only known via its pane.
	d.upsert("ses_known", session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})

	d.reconcilePanes(context.Background(), map[string]string{"ses_known": "%1", "ses_new": "%2"}, nil)

	pane := map[string]string{}
	for _, s := range reg.Snapshot() {
		pane[s.AgentSessionID] = s.Tmux.PaneID
	}
	if pane["ses_known"] != "%1" {
		t.Fatalf("known session should adopt its pane: %v", pane)
	}
	if pane["ses_new"] != "%2" {
		t.Fatalf("pane-only session should be created and controllable: %v", pane)
	}

	// Both panes vanish -> both revert to paneless (but stay in the list).
	d.reconcilePanes(context.Background(), map[string]string{}, nil)
	after := map[string]string{}
	present := map[string]bool{}
	for _, s := range reg.Snapshot() {
		after[s.AgentSessionID] = s.Tmux.PaneID
		present[s.AgentSessionID] = true
	}
	if after["ses_known"] != "" || after["ses_new"] != "" {
		t.Fatalf("detach should clear panes: %v", after)
	}
	if !present["ses_known"] || !present["ses_new"] {
		t.Fatalf("detached sessions must remain in the list: %v", present)
	}
}
