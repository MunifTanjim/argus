package node

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/memory"
	"github.com/MunifTanjim/argus/internal/transcript"
)

const (
	// captureBytes caps the transcript text of one extraction.
	captureBytes = 32 << 10
	// captureChunks caps the extractions of one session in one pass.
	captureChunks = 8
	// captureErrEvery is the shortest time between memory.error events of a
	// thread; capture of the thread waits that long after an error.
	captureErrEvery = time.Hour
)

// pagentCaptureEvery is how often idle chat sessions are checked for capture.
var pagentCaptureEvery = 30 * time.Second

func (d *Node) pagentCaptureLoop(ctx context.Context) {
	t := time.NewTicker(pagentCaptureEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.pagentCapture(ctx)
		}
	}
}

// defaultExtractor picks the extractor named by memory.extractor, else the
// agent's own CLI when it is claude or codex, else claude when installed.
func defaultExtractor(set pagent.Settings) memory.Extractor {
	if set.Extractor != "" {
		ex, err := memory.NewExtractor(set.Extractor)
		if err != nil {
			return nil
		}
		return ex
	}
	for _, cli := range []string{set.Agent, "claude"} {
		if cli != "claude" && cli != "codex" {
			continue
		}
		if cli == set.Agent {
			ex, _ := memory.NewExtractor(cli)
			return ex
		}
		if _, err := exec.LookPath(cli); err == nil {
			ex, _ := memory.NewExtractor(cli)
			return ex
		}
	}
	return nil
}

// captureBlocked reports whether capture of the thread is paused after an
// error.
func (d *Node) captureBlocked(threadID string) bool {
	d.captureMu.Lock()
	defer d.captureMu.Unlock()
	at, ok := d.captureErrAt[threadID]
	return ok && d.pagentNow().Sub(at) < captureErrEvery
}

// captureFailed records a capture error as a memory.error event, at most once
// per thread per captureErrEvery, and pauses capture of the thread.
func (d *Node) captureFailed(ctx context.Context, threadID string, err error) {
	d.captureReport(ctx, threadID, "capture failed: "+err.Error())
}

func (d *Node) captureReport(ctx context.Context, threadID, message string) {
	d.log.Warn("pagent: capture memory", "thread", threadID, "message", message)
	d.captureMu.Lock()
	if d.captureErrAt == nil {
		d.captureErrAt = map[string]time.Time{}
	}
	now := d.pagentNow()
	for id, at := range d.captureErrAt {
		if now.Sub(at) >= captureErrEvery {
			delete(d.captureErrAt, id)
		}
	}
	_, seen := d.captureErrAt[threadID]
	d.captureErrAt[threadID] = now
	d.captureMu.Unlock()
	if seen {
		return
	}
	d.memoryEvent(ctx, threadID, evMemoryError, map[string]string{"message": message})
}

// pagentCapture extracts memory from chat sessions that have been quiet for
// the agent's capture-after, or that ended, since their last capture.
func (d *Node) pagentCapture(ctx context.Context) {
	agents, err := d.pagents.ListAgents(ctx, false)
	if err != nil {
		d.log.Warn("pagent: list agents", "err", err)
		return
	}
	for _, a := range agents {
		if ctx.Err() != nil {
			return
		}
		set := d.pagentSettings(a)
		if set.MemoryMode == memoryModeOff {
			continue
		}
		threads, err := d.pagents.ListThreads(ctx, a.ID)
		if err != nil {
			continue
		}
		for _, th := range threads {
			if th.Kind != threadChat || th.StartedBy != startedByYou || th.Status != pagent.ThreadActive || d.captureBlocked(th.ID) {
				continue
			}
			d.captureThread(ctx, a, set, th)
		}
	}
}

func (d *Node) captureThread(ctx context.Context, a pagent.Agent, set pagent.Settings, th pagent.Thread) {
	links, err := d.pagents.ThreadSessions(ctx, th.ID)
	if err != nil {
		return
	}
	// A resumed session writes on in its predecessor's transcript: the
	// transcript is captured once, through its latest session, from the
	// furthest any of its sessions got.
	last := map[string]int{}
	for i, l := range links {
		if l.StartedBy == startedByYou && l.TranscriptPath != "" {
			last[l.TranscriptPath] = i
		}
	}
	for i, l := range links {
		if l.StartedBy != startedByYou || l.TranscriptPath == "" || last[l.TranscriptPath] != i {
			continue
		}
		cur, hasCur, err := d.pagents.CaptureCursor(ctx, th.ID, l.SessionID)
		if err != nil || !d.captureDue(l, cur, hasCur, set) {
			continue
		}
		for _, p := range links[:i] {
			if p.TranscriptPath != l.TranscriptPath {
				continue
			}
			if pc, ok, err := d.pagents.CaptureCursor(ctx, th.ID, p.SessionID); err == nil && ok && pc.EntryCount > cur.EntryCount {
				cur.EntryCount, cur.LastEntryID = pc.EntryCount, pc.LastEntryID
			}
		}
		if err := d.captureSession(ctx, a, set, th, l, cur); err != nil {
			d.captureFailed(ctx, th.ID, err)
			return
		}
	}
}

// captureDue reports whether the session has something to capture: it ended
// after the last capture, or has been idle since before capture-after and not
// captured since.
func (d *Node) captureDue(l pagent.ThreadSession, cur pagent.CaptureCursor, hasCur bool, set pagent.Settings) bool {
	if !l.EndedAt.IsZero() {
		return !hasCur || cur.UpdatedAt.Before(l.EndedAt)
	}
	s, live := d.linkSession(l)
	if !live {
		return !hasCur
	}
	since, idle := idleSince(s)
	if !idle || since.IsZero() || d.pagentNow().Sub(since) < set.CaptureAfter {
		return false
	}
	return !hasCur || cur.UpdatedAt.Before(since)
}

// captureSession reads the session's transcript after the cursor, extracts
// memory in chunks and applies it. The cursor moves only after a chunk
// succeeds.
func (d *Node) captureSession(ctx context.Context, a pagent.Agent, set pagent.Settings, th pagent.Thread, l pagent.ThreadSession, cur pagent.CaptureCursor) error {
	ad, ok := d.adapters[l.CLI]
	if !ok {
		return nil
	}
	view, err := ad.ReadTranscriptView(l.TranscriptPath)
	if err != nil {
		return err
	}
	entries := view.Entries
	start := cur.EntryCount
	if start > len(entries) || (start > 0 && entries[start-1].ID != cur.LastEntryID) {
		start = 0
	}
	cur.ThreadID, cur.SessionID = th.ID, l.SessionID

	for range captureChunks {
		chunk, end := captureChunk(entries, start)
		if len(chunk) > 0 {
			if err := d.captureExtract(ctx, a, set, th, chunk); err != nil {
				return err
			}
		}
		start = end
		cur.EntryCount, cur.LastEntryID, cur.UpdatedAt = start, "", d.pagentNow()
		if start > 0 {
			cur.LastEntryID = entries[start-1].ID
		}
		if err := d.pagents.SetCaptureCursor(ctx, cur); err != nil {
			return err
		}
		if start >= len(entries) {
			break
		}
	}
	return nil
}

// captureChunk returns the user and text entries from start on, up to
// captureBytes, and the index after the last entry consumed.
func captureChunk(entries []transcript.Entry, start int) ([]transcript.Entry, int) {
	var chunk []transcript.Entry
	size := 0
	for i := start; i < len(entries); i++ {
		e := entries[i]
		if e.Kind != transcript.EntryUser && e.Kind != transcript.EntryText {
			continue
		}
		e.Text = strings.TrimSpace(e.Text)
		if e.Text == "" {
			continue
		}
		if size+len(e.Text) > captureBytes {
			if len(chunk) > 0 {
				return chunk, i
			}
			e.Text = truncateUTF8(e.Text, captureBytes)
		}
		size += len(e.Text)
		chunk = append(chunk, e)
	}
	return chunk, len(entries)
}

func (d *Node) captureExtract(ctx context.Context, a pagent.Agent, set pagent.Settings, th pagent.Thread, chunk []transcript.Entry) error {
	ex := d.extractorFor(set)
	if ex == nil {
		if _, err := memory.NewExtractor(set.Extractor); set.Extractor != "" && err != nil {
			return fmt.Errorf("invalid memory.extractor: %s", set.Extractor)
		}
		return errors.New("no extractor available")
	}
	mem, user, topics, err := readMemoryContext(a)
	if err != nil {
		return err
	}

	reply, err := ex.Extract(ctx, d.redact(ctx, a.ID, memory.BuildCapturePrompt(mem, user, topics, chunk)))
	if err != nil {
		return err
	}
	res, err := memory.ParseCapture(reply)
	if err != nil {
		return err
	}
	var users []string
	for _, e := range chunk {
		if e.Kind == transcript.EntryUser {
			users = append(users, e.Text)
		}
	}
	ops := memory.FilterQuoted(res.Ops, users)
	if len(ops) == 0 && res.Log == "" {
		return nil
	}
	if _, err := d.pagentApplyMemory(ctx, a, th.ID, "capture", ops, res.Log); err != nil {
		var rpc *api.RPCError
		if errors.As(err, &rpc) && rpc.Code != api.CodeInternalError {
			// Retrying the same transcript cannot succeed; report and move on.
			d.captureReport(ctx, th.ID, "capture: "+rpc.Message)
			return nil
		}
		return err
	}
	return nil
}

// readMemoryContext reads the agent's memory, user file and topic file names
// (as memory-relative paths) for an extractor prompt.
func readMemoryContext(a pagent.Agent) (mem, user string, topics []string, err error) {
	memDir := filepath.Join(a.Dir, memoryDir)
	if mem, err = readFileOptional(filepath.Join(memDir, memoryFile)); err != nil {
		return
	}
	if user, err = readFileOptional(filepath.Join(memDir, userFile)); err != nil {
		return
	}
	files, _ := filepath.Glob(filepath.Join(memDir, "topics", "*.md"))
	for _, f := range files {
		topics = append(topics, "topics/"+filepath.Base(f))
	}
	slices.Sort(topics)
	return mem, user, topics, nil
}
