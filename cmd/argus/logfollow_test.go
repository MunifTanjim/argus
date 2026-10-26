package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type lineSink struct {
	mu     sync.Mutex
	writes []string
}

func (s *lineSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, string(p))
	return len(p), nil
}

func (s *lineSink) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.writes, "")
}

func (s *lineSink) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.text() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("log output = %q, want %q", s.text(), want)
}

func startFollow(t *testing.T, path string) *lineSink {
	t.Helper()
	old := logFollowInterval
	logFollowInterval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	sink := &lineSink{}
	go func() {
		defer close(done)
		followLog(ctx, path, sink)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		logFollowInterval = old
	})
	return sink
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func TestFollowLogWritesTailThenNewLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.log")
	appendFile(t, path, "a\nb\n")
	sink := startFollow(t, path)
	sink.waitFor(t, "a\nb\n")
	appendFile(t, path, "c\n")
	sink.waitFor(t, "a\nb\nc\n")
}

func TestFollowLogStartsAtFullLineWithinBacklog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.log")
	line := strings.Repeat("x", 99) + "\n"
	n := (logFollowBacklog / len(line)) + 10
	appendFile(t, path, strings.Repeat(line, n))
	sink := startFollow(t, path)
	deadline := time.Now().Add(5 * time.Second)
	for sink.text() == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	got := sink.text()
	if len(got) > logFollowBacklog || len(got) < logFollowBacklog-len(line) {
		t.Fatalf("backlog = %d bytes", len(got))
	}
	if strings.Count(got, line) != len(got)/len(line) {
		t.Fatal("backlog starts or ends in a partial line")
	}
}

func TestFollowLogContinuesAfterRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.log")
	appendFile(t, path, "a\n")
	sink := startFollow(t, path)
	sink.waitFor(t, "a\n")
	appendFile(t, path, "b\n")
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	appendFile(t, path, "d\n")
	sink.waitFor(t, "a\nb\nd\n")
}

func TestFollowLogContinuesAfterTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.log")
	appendFile(t, path, "aaaa\nbbbb\n")
	sink := startFollow(t, path)
	sink.waitFor(t, "aaaa\nbbbb\n")
	if err := os.WriteFile(path, []byte("c\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sink.waitFor(t, "aaaa\nbbbb\nc\n")
}

func TestFollowLogFlushesPartialLineOnTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.log")
	appendFile(t, path, "aaaa\npart")
	sink := startFollow(t, path)
	sink.waitFor(t, "aaaa\n")
	time.Sleep(30 * time.Millisecond)
	if err := os.WriteFile(path, []byte("c\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sink.waitFor(t, "aaaa\npart\nc\n")
}

func TestFollowLogWaitsForMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.log")
	sink := startFollow(t, path)
	time.Sleep(30 * time.Millisecond)
	appendFile(t, path, "a\nb\n")
	sink.waitFor(t, "a\nb\n")
}

func TestFollowLogHoldsPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.log")
	appendFile(t, path, "a\n")
	sink := startFollow(t, path)
	sink.waitFor(t, "a\n")
	appendFile(t, path, "e")
	time.Sleep(30 * time.Millisecond)
	if got := sink.text(); got != "a\n" {
		t.Fatalf("partial line emitted: %q", got)
	}
	appendFile(t, path, "f\n")
	sink.waitFor(t, "a\nef\n")
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.writes) != 2 || sink.writes[1] != "ef\n" {
		t.Fatalf("writes = %q", sink.writes)
	}
}
