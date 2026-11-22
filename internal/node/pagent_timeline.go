package node

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func (d *Node) handlePAgentThreadTimeline(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentTimelineParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.pagentStore()
	if err != nil {
		return nil, err
	}
	th, ok, err := s.Thread(ctx, p.ThreadID)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	if !ok {
		return nil, pagentErr("thread not found")
	}
	links, err := s.ThreadSessions(ctx, th.ID)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	events, err := s.Events(ctx, th.ID, 0)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	return d.buildTimeline(th.ID, links, events), nil
}

// timelineSegment is where one thread session's entries sit in the timeline.
// Sessions that share a transcript file share the segment of the first of
// them; from is where the session joined that segment.
type timelineSegment struct {
	start, n, from int
	entries        []transcript.Entry
}

func (d *Node) buildTimeline(threadID string, links []pagent.ThreadSession, events []pagent.Event) api.PAgentTimeline {
	res := api.PAgentTimeline{ThreadID: threadID, Entries: []transcript.Entry{}, Sessions: make([]api.PAgentTimelineSession, len(links))}
	segs := make([]timelineSegment, len(links))
	owners := map[string]int{}
	for i, l := range links {
		live := false
		path := l.TranscriptPath
		cli := l.CLI
		if sess, ok := d.linkSession(l); ok {
			live = true
			if sess.TranscriptPath != "" {
				path = sess.TranscriptPath
			}
		}
		res.Sessions[i] = api.PAgentTimelineSession{SessionID: l.SessionID, StartIndex: len(res.Entries), Live: live,
			CLI: cli, AgentSessionID: l.AgentSessionID, TranscriptPath: path}
		if path == "" {
			segs[i] = timelineSegment{start: len(res.Entries)}
			continue
		}
		if o, shared := owners[path]; shared {
			seg := segs[o]
			seg.from = firstAfter(seg.entries, l.StartedAt)
			segs[i] = seg
			continue
		}
		owners[path] = i
		seg := timelineSegment{start: len(res.Entries)}
		view, err := d.adapterFor(cli).ReadTranscriptView(path)
		if err != nil {
			d.log.Warn("pagent: read thread transcript", "path", path, "err", err)
			view.Entries = []transcript.Entry{{
				ID: "err", Kind: transcript.EntryArgus, Label: "error", IsError: true,
				Detail: fmt.Sprintf("transcript unavailable: %v", err),
			}}
		}
		for _, e := range view.Entries {
			e.ID = fmt.Sprintf("s%d:%s", i, e.ID)
			seg.entries = append(seg.entries, e)
		}
		seg.n = len(seg.entries)
		segs[i] = seg
		res.Entries = append(res.Entries, seg.entries...)
	}

	type placed struct {
		pos   int
		entry transcript.Entry
		id    int64
	}
	ins := make([]placed, 0, len(events))
	for _, ev := range events {
		pos := len(res.Entries)
		if i := slices.IndexFunc(links, func(l pagent.ThreadSession) bool { return l.SessionID == ev.AnchorSession }); ev.AnchorSession != "" && i >= 0 {
			seg := segs[i]
			pos = seg.start + min(seg.from+max(ev.AnchorIndex, 0), seg.n)
		} else if i := lastStartedBy(links, ev.CreatedAt); i >= 0 {
			seg := segs[i]
			pos = seg.start + max(firstAfter(seg.entries, ev.CreatedAt), seg.from)
		}
		ins = append(ins, placed{pos, transcript.Entry{
			ID: fmt.Sprintf("ev:%d", ev.ID), Kind: transcript.EntryArgus,
			Timestamp: ev.CreatedAt.UTC().Format(time.RFC3339Nano), Label: ev.Kind, Detail: eventText(ev),
			Event: &transcript.ArgusEvent{ID: ev.ID, Kind: ev.Kind, Payload: ev.Payload},
		}, ev.ID})
	}
	slices.SortStableFunc(ins, func(a, b placed) int {
		if a.pos != b.pos {
			return a.pos - b.pos
		}
		return int(a.id - b.id)
	})
	merged := make([]transcript.Entry, 0, len(res.Entries)+len(ins))
	next := 0
	for i, e := range res.Entries {
		for next < len(ins) && ins[next].pos <= i {
			merged = append(merged, ins[next].entry)
			next++
		}
		merged = append(merged, e)
	}
	for ; next < len(ins); next++ {
		merged = append(merged, ins[next].entry)
	}
	res.Entries = merged

	// Session start indexes shift by the events spliced in before them.
	for i := range res.Sessions {
		shift := 0
		for _, p := range ins {
			if p.pos < res.Sessions[i].StartIndex {
				shift++
			}
		}
		res.Sessions[i].StartIndex += shift
	}
	return res
}

// firstAfter returns the index of the first entry stamped after t, or the
// number of entries when there is none.
func firstAfter(entries []transcript.Entry, t time.Time) int {
	if t.IsZero() {
		return len(entries)
	}
	for i, e := range entries {
		if ts, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil && ts.After(t) {
			return i
		}
	}
	return len(entries)
}

// lastStartedBy returns the index of the last link started at or before t.
func lastStartedBy(links []pagent.ThreadSession, t time.Time) int {
	for i, l := range slices.Backward(links) {
		if !l.StartedAt.After(t) {
			return i
		}
	}
	return -1
}

func eventText(ev pagent.Event) string {
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(ev.Payload, &raw)
	m := map[string]string{}
	for k, v := range raw {
		var s string
		if json.Unmarshal(v, &s) == nil {
			m[k] = s
		}
	}
	switch ev.Kind {
	case evMemoryChanged:
		return "Memory: " + cmp.Or(m["summary"], "changed")
	case evMemoryProposed:
		var ops []json.RawMessage
		_ = json.Unmarshal(raw["ops"], &ops)
		if len(ops) == 1 {
			return "Memory: 1 change to review"
		}
		return fmt.Sprintf("Memory: %d changes to review", len(ops))
	case evMemoryBlocked:
		return fmt.Sprintf("Memory: not saved, looks like a secret (%s)", m["rule"])
	case evMemoryError:
		return "Memory: " + cmp.Or(m["message"], "error")
	case "session.started":
		return "Session started"
	case "session.resumed":
		return "Session resumed"
	case "session.continued":
		if r := m["reason"]; r != "" {
			return "Continued in a new session: " + r
		}
		return "Continued in a new session"
	case "session.idle_closed":
		return "Session closed after being idle"
	}
	if m["message"] != "" {
		return m["message"]
	}
	return ev.Kind
}
