package opencode

import (
	"context"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/transcript"
)

var serviceDial func() (*client, bool) // test override

func serviceClient() (*client, bool) {
	if serviceDial != nil {
		return serviceDial()
	}
	info, ok := readServiceInfo()
	if !ok {
		return nil, false
	}
	return newClient(info), true
}

func readTranscriptView(sessionID string, finished bool) (transcript.TranscriptView, error) {
	c, ok := serviceClient()
	if !ok {
		return transcript.TranscriptView{}, nil
	}
	items, err := c.readMessages(context.Background(), sessionID)
	if err != nil {
		return transcript.TranscriptView{}, err
	}
	return transcript.TranscriptView{Entries: foldMessages(items, finished)}, nil
}

func findToolDetail(sessionID, agentID, toolID string) (transcript.ToolDetail, bool, error) {
	// A subagent's tools live in its own child session, not the parent transcript.
	if agentID != "" {
		sessionID = agentID
	}
	view, err := readTranscriptView(sessionID, false)
	if err != nil {
		return transcript.ToolDetail{}, false, err
	}
	d, ok := transcript.FindToolDetail(view.Entries, toolID)
	return d, ok, nil
}

func subagentFilePath(_, agentID string) (string, bool) { return agentID, agentID != "" }

func readSubagentView(_, agentID string, finished bool) (transcript.TranscriptView, bool, error) {
	view, err := readTranscriptView(agentID, finished)
	if err != nil {
		return transcript.TranscriptView{}, false, err
	}
	return view, len(view.Entries) > 0, nil
}

// transcriptSig returns a monotone signature capturing both entry count and
// intra-entry content growth (longer tool results, accumulated text).
// It can never return -1, so -1 is safe as the uninitialized sentinel.
func transcriptSig(entries []transcript.Entry) int {
	n := len(entries)
	for _, e := range entries {
		n += len(e.Text) + len(e.Result) + len(e.ToolInput)
	}
	return n
}

type streamingTranscript struct {
	sessionID string
	entries   []transcript.Entry
	sig       int
	readView  func(string) (transcript.TranscriptView, error)
}

func newStreamingTranscript(path, _ string, _ bool) adapter.StreamingTranscript {
	return &streamingTranscript{
		sessionID: path,
		sig:       -1,
		readView:  func(id string) (transcript.TranscriptView, error) { return readTranscriptView(id, false) },
	}
}

func (s *streamingTranscript) Refresh() ([]transcript.Entry, error) {
	view, err := s.readView(s.sessionID)
	if err != nil {
		return s.entries, nil // transient; keep last good
	}
	sig := transcriptSig(view.Entries)
	if sig == s.sig {
		return s.entries, nil
	}
	s.sig = sig
	s.entries = view.Entries
	return s.entries, nil
}
