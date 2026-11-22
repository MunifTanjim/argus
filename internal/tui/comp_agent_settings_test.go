package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func setCalls(cl *pagentClient) []api.PAgentSetParams {
	var out []api.PAgentSetParams
	for i, call := range cl.calls {
		if call == api.MethodPAgentSet {
			out = append(out, cl.params[i].(api.PAgentSetParams))
		}
	}
	return out
}

func TestAgentViewUsageAndSettings(t *testing.T) {
	cl := agentViewClient()
	cl.usage = &api.PAgentUsageResult{Total: api.PAgentUsageRow{Sessions: 12, Cost: 3.4, Tokens: 45000}}
	m := calmAgentsModel()
	for i := range m.pagents {
		if m.pagents[i].ID == "n1:u" {
			m.pagents[i].Settings = map[string]api.PAgentSetting{
				"notify":      {Value: "needs-you"},
				"proactivity": {Value: "high", Explicit: true},
				"memory_mode": {Value: "auto"},
			}
		}
	}
	m.client = cl
	c := &ctx{m: &m}
	cmd := openAgent(c, "n1:u")
	m.apply(c)
	m, _ = upd(m, cmd())
	out := ansi.Strip(m.baseComp().view(&ctx{m: &m}, 100, 40))
	for _, want := range []string{"Usage", "Last 7 days · 12 sessions · $3.40 · 45.0k tokens", "Settings",
		"Notifications  needs-you (default)", "Proactivity    high", "Memory         auto (default)"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "high (default)") {
		t.Error("an explicit setting is marked default")
	}

	// zs off a setting says so; on Notifications it moves to the first option.
	if mm, cmd := typeKeysCmd(m, "zs"); cmd != nil || mm.flash != "pick a setting" {
		t.Fatalf("zs on a thread: flash %q", mm.flash)
	}
	m = typeKeys(m, "G")  // Memory
	m = typeKeys(m, "kk") // Notifications
	m, cmd = typeKeysCmd(m, "zs")
	m, _ = upd(m, cmd())
	m = typeKeys(m, "j") // Proactivity: high is last, so it goes back to the default
	m, cmd = typeKeysCmd(m, "zs")
	m, _ = upd(m, cmd())
	m, cmd = upd(m, cmdMsg("agent set memory review"))
	m, _ = upd(m, cmd())
	m, cmd = upd(m, cmdMsg("agent unset notify"))
	m, _ = upd(m, cmd())
	want := []api.PAgentSetParams{
		{AgentID: "n1:u", Key: "notify", Value: "all"},
		{AgentID: "n1:u", Key: "proactivity", Value: ""},
		{AgentID: "n1:u", Key: "memory.mode", Value: "review"},
		{AgentID: "n1:u", Key: "notify", Value: ""},
	}
	got := setCalls(cl)
	if len(got) != len(want) {
		t.Fatalf("set calls = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("set call %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if m.flash != "notify: default" {
		t.Errorf("flash = %q", m.flash)
	}
}

func offerEntries() []transcript.Entry {
	ts := func(m int) string { return threadT0.Add(time.Duration(m) * time.Minute).Format(time.RFC3339Nano) }
	return []transcript.Entry{
		{ID: "ev:1", Kind: transcript.EntryArgus, Label: "brief", Detail: "Good morning.\nTwo PRs wait for you.", Timestamp: ts(0),
			Event: &transcript.ArgusEvent{ID: 1, Kind: "brief", Payload: json.RawMessage(`{"text":"Good morning.\nTwo PRs wait for you."}`)}},
		{ID: "ev:2", Kind: transcript.EntryArgus, Label: "offer", Detail: "Offer: tidy the README", Timestamp: ts(1),
			Event: &transcript.ArgusEvent{ID: 2, Kind: "offer", Payload: json.RawMessage(`{"offer_id":"of_1","title":"tidy the README"}`)}},
		{ID: "ev:3", Kind: transcript.EntryArgus, Label: "offer", Detail: "Offer: bump deps", Timestamp: ts(2),
			Event: &transcript.ArgusEvent{ID: 3, Kind: "offer", Payload: json.RawMessage(`{"offer_id":"of_2","title":"bump deps"}`)}},
		{ID: "ev:4", Kind: transcript.EntryArgus, Label: "offer.dismissed", Detail: "Dismissed the offer: bump deps", Timestamp: ts(3),
			Event: &transcript.ArgusEvent{ID: 4, Kind: "offer.dismissed", Payload: json.RawMessage(`{"offer_id":"of_2"}`)}},
	}
}

func TestThreadBriefAndOffers(t *testing.T) {
	cl := threadClient()
	cl.timeline.Entries = offerEntries()
	m := openedThread(t, cl)
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"Brief", "Good morning.", "Two PRs wait for you.", "Offer: tidy the README  a start · x dismiss"} {
		if !strings.Contains(out, want) {
			t.Errorf("thread view lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "bump deps  a start") {
		t.Error("a dismissed offer still offers start")
	}
	// The cursor is on the last entry: not an offer.
	if mm, cmd := upd(m, keyMsg("a")); cmd != nil || mm.flash != "not an open offer" {
		t.Fatalf("a off an offer: flash %q", mm.flash)
	}
	m = typeKeys(m, "k") // the dismissed offer
	if mm, cmd := upd(m, keyMsg("a")); cmd != nil || mm.flash != "not an open offer" {
		t.Fatalf("a on a dismissed offer: flash %q", mm.flash)
	}
	m = typeKeys(m, "k") // the open offer
	m, cmd := upd(m, keyMsg("a"))
	cmd()
	m, cmd = upd(m, keyMsg("x"))
	cmd()
	var got []string
	for i, call := range cl.calls {
		if call == api.MethodPAgentOfferStart || call == api.MethodPAgentOfferDismiss {
			p := cl.params[i].(api.PAgentOfferParams)
			got = append(got, call+" "+p.ThreadID+" "+p.OfferID)
		}
	}
	if strings.Join(got, ",") != api.MethodPAgentOfferStart+" n1:t1 of_1,"+api.MethodPAgentOfferDismiss+" n1:t1 of_1" {
		t.Fatalf("offer calls = %v", got)
	}
}
