package logbuf

import (
	"fmt"
	"sync"
	"testing"
)

func TestEvictsOldestBeyondMax(t *testing.T) {
	b := New(2)
	for i := 0; i < 3; i++ {
		fmt.Fprintf(b, "line %d\n", i)
	}
	got := b.Lines()
	want := []string{"line 1", "line 2"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestPartialLineHeldUntilNewline(t *testing.T) {
	b := New(10)
	if _, err := b.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	if b.Len() != 0 {
		t.Fatalf("Len after partial = %d, want 0", b.Len())
	}
	if _, err := b.Write([]byte("c\n")); err != nil {
		t.Fatal(err)
	}
	got := b.Lines()
	if len(got) != 1 || got[0] != "abc" {
		t.Fatalf("Lines = %v, want [abc]", got)
	}
}

func TestNotifyCoalesces(t *testing.T) {
	b := New(10)
	fmt.Fprintf(b, "one\n")
	fmt.Fprintf(b, "two\n")
	// Two writes, single-slot channel: exactly one pending signal, then empty.
	select {
	case <-b.Notify():
	default:
		t.Fatal("expected one pending notify")
	}
	select {
	case <-b.Notify():
		t.Fatal("expected notify to be coalesced to one")
	default:
	}
}

func TestConcurrentWriteAndRead(t *testing.T) {
	b := New(100)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			fmt.Fprintf(b, "line %d\n", i)
		}
	}()
	for i := 0; i < 1000; i++ {
		_ = b.Lines()
		_ = b.Len()
	}
	wg.Wait()
	if b.Len() != 100 {
		t.Fatalf("final Len = %d, want 100", b.Len())
	}
}
