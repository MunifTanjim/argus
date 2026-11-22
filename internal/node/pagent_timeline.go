package node

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
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
	var m map[string]any
	_ = json.Unmarshal(ev.Payload, &m)
	str := func(k string) string {
		v, _ := m[k].(string)
		return strings.TrimSpace(v)
	}
	list := func(k string) []string {
		vs, _ := m[k].([]any)
		var out []string
		for _, v := range vs {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	// with appends more, its first line, after a colon.
	with := func(s, more string) string {
		more, _, _ = strings.Cut(strings.TrimSpace(more), "\n")
		if more == "" {
			return s
		}
		return s + ": " + more
	}
	routine := "Routine " + cmp.Or(str("routine"), "routine")
	switch ev.Kind {
	case evMemoryChanged:
		return "Memory: " + cmp.Or(str("summary"), "changed")
	case evMemoryProposed:
		ops, _ := m["ops"].([]any)
		if len(ops) == 1 {
			return "Memory: 1 change to review"
		}
		return fmt.Sprintf("Memory: %d changes to review", len(ops))
	case evMemoryBlocked:
		return fmt.Sprintf("Memory: not saved, looks like a secret (%s)", str("rule"))
	case evMemoryError:
		return "Memory: " + cmp.Or(str("message"), "error")
	case "session.started":
		return "Session started"
	case "session.resumed":
		return "Session resumed"
	case "session.continued":
		return with("Continued in a new session", str("reason"))
	case "session.idle_closed":
		return "Session closed after being idle"
	case evRoutineResult:
		return with(routine+" "+cmp.Or(str("result"), "finished"), str("summary"))
	case evRoutineNeedsYou:
		return with(routine+" needs you", str("summary"))
	case evRoutineCompleted:
		return routine + " reached its goal"
	case evRoutineFailing:
		return with(routine+" failed", str("error"))
	case evRoutineChanged:
		return routine + " " + cmp.Or(str("action"), "changed")
	case evRoutineProposed:
		return with(routine+" change to review", str("reason"))
	case evRoutineError:
		return with(routine+" error", str("message"))
	case evRoutineMissed:
		n, _ := m["count"].(float64)
		if n <= 1 {
			return routine + " missed 1 run"
		}
		return fmt.Sprintf("%s missed %d runs", routine, int(n))
	case evRoutineSkipped:
		return with(routine+" skipped a run", str("reason"))
	case evRoutineStale:
		s := routine + " is overdue"
		if t, err := time.Parse(time.RFC3339, str("due")); err == nil {
			s += " since " + t.UTC().Format("2006-01-02 15:04 UTC")
		}
		return s
	case evRoutinePaused:
		names := cmp.Or(strings.Join(list("routines"), ", "), str("routine"), "routines")
		return with(with("Paused "+names, str("reason")), str("message"))
	case evDraft:
		return with("Drafted "+cmp.Or(str("tool"), "an action"), cmp.Or(str("command"), str("target")))
	case evDraftSent:
		switch {
		case str("error") != "":
			return with("Approved action failed", str("error"))
		case str("command") != "":
			return with("Ran approved command", str("command"))
		}
		return "Sent the approved action to the agent"
	case evDraftDiscarded:
		return "Discarded a drafted action"
	case evHandoffAsked:
		return with("Asked "+cmp.Or(str("agent"), "an agent"), str("task"))
	case evHandoffAnswered:
		// The answer is kept word for word.
		s := "Answer from " + cmp.Or(str("agent"), "an agent")
		if t := strings.TrimSpace(str("text")); t != "" {
			s += ": " + t
		}
		return s
	case evHandoffRefused:
		return cmp.Or(str("reason"), "Could not ask "+cmp.Or(str("agent"), "the agent"))
	case evHandoffNoAnswer:
		return with("No answer from "+cmp.Or(str("agent"), "the agent"), str("text"))
	}
	return cmp.Or(str("message"), ev.Kind)
}
