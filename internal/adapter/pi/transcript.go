package pi

import (
	"os"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// readTranscriptView folds the active branch of the session file at path.
// Pi has no subagent files (it ships without sub-agents), so agent drill-down
// is unsupported: readSubagentView always reports absent.
func readTranscriptView(path string, finished bool) (transcript.TranscriptView, error) {
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

// streamSig is a monotone signature over entry count plus intra-entry growth,
// so Refresh notices appended text and tool results, not just new rows.
type streamSig struct {
	mtime time.Time
	size  int64
	sig   int
}

func sigOf(entries []transcript.Entry, mtime time.Time, size int64) streamSig {
	n := len(entries)
	for _, e := range entries {
		n += len(e.Text) + len(e.Result) + len(e.ToolInput)
	}
	return streamSig{mtime: mtime, size: size, sig: n}
}

type streamingTranscript struct {
	path    string
	entries []transcript.Entry
	last    streamSig
	init    bool
}

func newStreamingTranscript(path, _ string, _ bool) adapter.StreamingTranscript {
	return &streamingTranscript{path: path}
}

func (s *streamingTranscript) Refresh() ([]transcript.Entry, error) {
	info, err := os.Stat(s.path)
	if err != nil {
		// Not yet persisted (or rotated away): keep last good, like opencode
		// does on transient service errors.
		return s.entries, nil
	}
	view, err := readTranscriptView(s.path, false)
	if err != nil {
		return s.entries, nil
	}
	sig := sigOf(view.Entries, info.ModTime(), info.Size())
	if s.init && sig == s.last {
		return s.entries, nil
	}
	s.init, s.last, s.entries = true, sig, view.Entries
	return s.entries, nil
}
