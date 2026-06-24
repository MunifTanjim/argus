package clienttoken

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustToken(t *testing.T) string {
	t.Helper()
	tok, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if !validToken(tok) {
		t.Fatalf("generated token %q is not valid", tok)
	}
	return tok
}

func TestAuthorizePromotesPendingAndWritesFile(t *testing.T) {
	s := New(t.TempDir())
	tok := mustToken(t)

	ch := s.Pend(tok)
	if _, err := os.Stat(s.path(tok)); !os.IsNotExist(err) {
		t.Fatalf("pending token must not have a file yet")
	}

	if !s.Authorize(tok) {
		t.Fatalf("Authorize on a pending token should succeed")
	}
	select {
	case <-ch:
	default:
		t.Fatalf("waiter channel should be closed after Authorize")
	}
	if _, err := os.Stat(s.path(tok)); err != nil {
		t.Fatalf("token file should exist after promotion: %v", err)
	}
	// Still authorized once active (file-backed).
	if !s.Authorize(tok) {
		t.Fatalf("active token should remain authorized")
	}
}

func TestAuthorizeRejectsUnknownAndNonHex(t *testing.T) {
	s := New(t.TempDir())
	if s.Authorize(mustToken(t)) {
		t.Fatalf("unknown token must be rejected")
	}
	for _, bad := range []string{"", "../etc/passwd", "ZZZZ", "abc"} {
		if validToken(bad) {
			t.Fatalf("validToken(%q) should be false", bad)
		}
		if s.Authorize(bad) {
			t.Fatalf("Authorize(%q) must be false", bad)
		}
	}
}

func TestListAndRemoveRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	tok := mustToken(t)
	s.Pend(tok)
	if !s.Authorize(tok) {
		t.Fatalf("authorize failed")
	}

	recs, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(recs) != 1 || recs[0].Token != tok {
		t.Fatalf("List = %+v, want one record for %s", recs, tok)
	}
	if recs[0].CreatedAt == "" {
		t.Fatalf("record should carry a created_at")
	}

	if err := s.Remove(tok); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if s.Authorize(tok) {
		t.Fatalf("token should not authorize after removal")
	}
	recs, _ = s.List()
	if len(recs) != 0 {
		t.Fatalf("List after remove = %+v, want empty", recs)
	}
}

func TestListEmptyDirIsNotError(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "does-not-exist"))
	recs, err := s.List()
	if err != nil {
		t.Fatalf("List on missing dir: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("want empty, got %+v", recs)
	}
}

func TestCancelPendStopsAuthorization(t *testing.T) {
	s := New(t.TempDir())
	tok := mustToken(t)
	s.Pend(tok)
	s.CancelPend(tok)
	if s.Authorize(tok) {
		t.Fatalf("cancelled pending token must not authorize")
	}
}

func TestCreatedAtUsesInjectedClock(t *testing.T) {
	s := New(t.TempDir())
	fixed := time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fixed }
	tok := mustToken(t)
	s.Pend(tok)
	s.Authorize(tok)
	recs, _ := s.List()
	if len(recs) != 1 || recs[0].CreatedAt != fixed.Format(time.RFC3339) {
		t.Fatalf("created_at = %q, want %q", recs[0].CreatedAt, fixed.Format(time.RFC3339))
	}
}
