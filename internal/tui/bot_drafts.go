package tui

import (
	"encoding/json"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// draftEventID is the event id of a draft entry, or 0.
func draftEventID(e transcript.Entry) int64 {
	if e.Kind != transcript.EntryArgus || e.Event == nil || e.Event.Kind != "draft" {
		return 0
	}
	return e.Event.ID
}

// draftOpen reports whether no entry sent or discarded draft id.
func draftOpen(es []transcript.Entry, id int64) bool {
	for _, e := range es {
		if e.Event == nil || (e.Event.Kind != "draft.sent" && e.Event.Kind != "draft.discarded") {
			continue
		}
		var p struct {
			EventID int64 `json:"event_id"`
		}
		if json.Unmarshal(e.Event.Payload, &p) == nil && p.EventID == id {
			return false
		}
	}
	return true
}

// draftHint is the keys of an open draft entry, or "".
func (m tview) draftHint(e transcript.Entry) string {
	id := draftEventID(e)
	if id == 0 || !draftOpen(m.transcript.entries, id) || !threadHasStartDismiss() {
		return ""
	}
	return "  " + StyleDim.Render(m.c.m.keyTextOn("thread", offerKeys.Start)+" send · "+
		m.c.m.keyTextOn("thread", offerKeys.Dismiss)+" discard")
}

// threadHasStartDismiss reports whether the thread view maps the start and
// dismiss keys, so an offer or draft can show them.
func threadHasStartDismiss() bool {
	return screenHas("thread", offerKeys.Start.name) && screenHas("thread", offerKeys.Dismiss.name)
}

// draftAt is the open draft under the cursor, or 0.
func (t threadComp) draftAt(v tview) int64 {
	r, ok := v.cursorRow()
	if !ok || r.kind != entryRow {
		return 0
	}
	es := t.tr.transcript.entries
	if id := draftEventID(es[r.entry]); id != 0 && draftOpen(es, id) {
		return id
	}
	return 0
}

// draftCmd sends or discards a draft in thread.
func (m model) draftCmd(threadID string, eventID int64, send bool) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		p := api.BotDraftParams{ThreadID: threadID, EventID: eventID}
		if send {
			return threadActionMsg{threadID: threadID, verb: "send draft", err: client.Call(api.MethodBotDraftSend, p, nil)}
		}
		return threadActionMsg{threadID: threadID, verb: "discard draft", err: client.Call(api.MethodBotDraftDiscard, p, nil)}
	}
}

// closedEntryFlash says why the start and dismiss keys did nothing on the
// row under the cursor: a draft or an offer that is no longer open.
func (t threadComp) closedEntryFlash(v tview) string {
	if r, ok := v.cursorRow(); ok && r.kind == entryRow {
		if e := t.tr.transcript.entries[r.entry]; e.Event != nil && e.Event.Kind == "draft" {
			return "not an open draft"
		}
	}
	return "not an open offer"
}
