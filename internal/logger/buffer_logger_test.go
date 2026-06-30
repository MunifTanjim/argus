package logger

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// ansiEscape matches SGR color escapes so assertions can compare the plain text
// that tint colorizes (e.g. the faded attribute keys).
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestNewBufferLoggerWritesFormattedRecord(t *testing.T) {
	var buf bytes.Buffer
	l := NewBufferLogger(&buf)
	l.Info("hello world", "err", "boom")
	out := buf.String()

	// Color is on (the Logs tab wants parity with stderr: colored level, faded
	// keys), so assert on the ANSI-stripped text for the content checks.
	plain := ansiEscape.ReplaceAllString(out, "")
	if !strings.Contains(plain, "hello world") {
		t.Errorf("output missing message: %q", out)
	}
	if !strings.Contains(plain, "err=boom") {
		t.Errorf("output missing attr: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("record should end with newline: %q", out)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("expected ANSI color escapes in output, got none: %q", out)
	}
}
