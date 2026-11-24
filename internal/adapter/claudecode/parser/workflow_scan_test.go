package parser

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const workflowFixture = "testdata/workflow-parent.jsonl"

func TestWorkflowScannerLaunches(t *testing.T) {
	s := NewWorkflowScanner(workflowFixture)
	if err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	ls := s.Launches()
	if len(ls) != 4 {
		t.Fatalf("launches = %d, want 4", len(ls))
	}
	if l := ls[0]; l.RunID != "wf_fd74c488-8d1" || l.Name != "final-branch-review" || l.End != "completed" || l.Remote {
		t.Errorf("first = %+v", l)
	}
	if ls[0].LaunchedAt.IsZero() {
		t.Error("first LaunchedAt not set")
	}
	if ls[1].End != "" || ls[1].RunID != "wf_bbbbbbbb-001" {
		t.Errorf("second = %+v", ls[1])
	}
	if ls[2].End != "failed" {
		t.Errorf("third = %+v", ls[2])
	}
	if !ls[3].Remote || ls[3].RunID != "" {
		t.Errorf("fourth = %+v", ls[3])
	}
	if l, ok := s.Launch(ls[0].ToolID); !ok || l.RunID != ls[0].RunID {
		t.Errorf("Launch lookup = %+v, %v", l, ok)
	}
	if _, ok := s.Launch("nope"); ok {
		t.Error("Launch(nope) found")
	}
}

func TestWorkflowScannerPartialLine(t *testing.T) {
	data, err := os.ReadFile(workflowFixture)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	// lines[3] is the notification user line (lines[2] is the queue-operation).
	prefix := bytes.Join(lines[:3], nil)
	notif := bytes.TrimSuffix(lines[3], []byte("\n"))

	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, append(prefix, notif...), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewWorkflowScanner(path)
	if err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	if got := s.Launches()[0].End; got != "" {
		t.Fatalf("End before newline = %q, want empty", got)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	if got := s.Launches()[0].End; got != "completed" {
		t.Fatalf("End after newline = %q, want completed", got)
	}
}

// TestWorkflowScannerLongLines: lines longer than the read buffer are read
// whole, including a partial one completed by a later append.
func TestWorkflowScannerLongLines(t *testing.T) {
	data, err := os.ReadFile(workflowFixture)
	if err != nil {
		t.Fatal(err)
	}
	pad := []byte(`{"pad":"` + strings.Repeat("x", 3*initialBufSize) + `",`)
	lines := bytes.SplitAfter(data, []byte("\n"))
	for i := range lines[:4] {
		lines[i] = append(append([]byte(nil), pad...), lines[i][1:]...)
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	full := bytes.Join(lines[:4], nil)
	if err := os.WriteFile(path, full[:len(full)-1], 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewWorkflowScanner(path)
	if err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	ls := s.Launches()
	if len(ls) != 1 || ls[0].RunID != "wf_fd74c488-8d1" || ls[0].End != "" {
		t.Fatalf("before newline: %+v", ls)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	if got := s.Launches()[0].End; got != "completed" {
		t.Fatalf("End after newline = %q, want completed", got)
	}
}

func TestWorkflowScannerQueueOperationIgnored(t *testing.T) {
	data, err := os.ReadFile(workflowFixture)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, bytes.Join(lines[:3], nil), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewWorkflowScanner(path)
	if err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	if got := s.Launches()[0].End; got != "" {
		t.Fatalf("End from queue-operation = %q, want empty", got)
	}
}

func TestWorkflowScannerTruncation(t *testing.T) {
	data, err := os.ReadFile(workflowFixture)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewWorkflowScanner(path)
	if err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	// Keep only the first launch's tool_use line, before its tool_result.
	first := data[:bytes.IndexByte(data, '\n')+1]
	if err := os.WriteFile(path, first, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	ls := s.Launches()
	if len(ls) != 1 {
		t.Fatalf("launches = %d, want 1", len(ls))
	}
	if ls[0].RunID != "" || ls[0].End != "" {
		t.Errorf("launch = %+v, want no result", ls[0])
	}
}

func TestMapWorkflowEnd(t *testing.T) {
	for in, want := range map[string]string{
		"completed": "completed",
		"failed":    "failed",
		"killed":    "stopped",
		"weird":     "weird",
	} {
		if got := MapWorkflowEnd(in); got != want {
			t.Errorf("MapWorkflowEnd(%q) = %q, want %q", in, got, want)
		}
	}
}
