package pi

import (
	"os"
	"slices"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// readTranscriptView folds the active branch of the session file at path.
// Pi has no subagent files (it ships without sub-agents), so agent drill-down
// is unsupported: readSubagentView always reports absent.
func readTranscriptView(path string, finished bool) (transcript.TranscriptView, error) {
	if path == "" {
		// Foreign sessions report no file until pi persists one.
		return transcript.TranscriptView{}, nil
	}
	sf, err := readSessionFile(path)
	if err != nil {
		return transcript.TranscriptView{}, err
	}
	return transcript.TranscriptView{Entries: foldBranch(sf.activeBranch(), finished)}, nil
}

func readSubagentView(_, _ string, _ bool) (transcript.TranscriptView, bool, error) {
	return transcript.TranscriptView{}, false, nil
}

func subagentFilePath(_, _ string) (string, bool) { return "", false }

func findToolDetail(path, _, toolID string) (transcript.ToolDetail, bool, error) {
	view, err := readTranscriptView(path, false)
	if err != nil {
		return transcript.ToolDetail{}, false, err
	}
	d, ok := transcript.FindToolDetail(view.Entries, toolID)
	return d, ok, nil
}

type fileStat struct {
	mtime time.Time
	size  int64
}

type streamingTranscript struct {
	path    string
	file    sessionFile
	entries []transcript.Entry
	last    fileStat // zero until the first read: no file matches it

	queue     func() []string // pi's live prompt queue; nil without a node
	dismissed []string        // queue as of ClearQueue, hidden until it changes
}

var _ adapter.QueueSource = (*streamingTranscript)(nil)

func newStreamingTranscript(path string) *streamingTranscript {
	return &streamingTranscript{path: path}
}

// Queue reports the prompts pi has queued for the session: live from RPC
// queue updates, or for a foreign session, the prompts Argus sent that pi has
// not taken yet.
func (s *streamingTranscript) Queue() []transcript.QueuedPrompt {
	if s.queue == nil {
		return nil
	}
	live := s.queue()
	if s.dismissed != nil && slices.Equal(live, s.dismissed) {
		return nil
	}
	s.dismissed = nil
	var q []transcript.QueuedPrompt
	for _, text := range live {
		q = append(q, transcript.QueuedPrompt{Text: text})
	}
	return q
}

// ClearQueue hides the current queue until pi reports a different one.
func (s *streamingTranscript) ClearQueue() {
	if s.queue != nil {
		s.dismissed = slices.Clone(s.queue())
	}
}

func (s *streamingTranscript) Refresh() ([]transcript.Entry, error) {
	info, err := os.Stat(s.path)
	if err != nil {
		// Not yet persisted (or rotated away): keep last good.
		return s.entries, nil
	}
	st := fileStat{mtime: info.ModTime(), size: info.Size()}
	if st == s.last {
		return s.entries, nil
	}
	if s.file.update(s.path) != nil {
		return s.entries, nil
	}
	s.last, s.entries = st, foldBranch(s.file.activeBranch(), false)
	return s.entries, nil
}
