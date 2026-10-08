package node

import (
	"context"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

type fakeStream struct{}

func (fakeStream) Refresh() ([]transcript.Entry, error) { return nil, nil }

func newFakeStream() adapter.StreamingTranscript { return fakeStream{} }

func (c *transcriptCache) has(key transcriptKey) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.items[key]
	return ok
}

func waitTranscriptEvicted(t *testing.T, c *transcriptCache, key transcriptKey) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.has(key) {
		if time.Now().After(deadline) {
			t.Fatalf("%v not evicted", key)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTranscriptCacheSharesItem(t *testing.T) {
	c := newTranscriptCache(8, time.Hour)
	key := transcriptKey{path: "/a"}
	a := c.acquire(key, newFakeStream)
	b := c.acquire(key, newFakeStream)
	if a != b {
		t.Fatal("two acquires of one key should share the item")
	}
	if c.acquire(transcriptKey{path: "/a", isSubagent: true}, newFakeStream) == a {
		t.Fatal("subagent flag should be part of the key")
	}
}

func TestTranscriptCacheKeepsReleasedItem(t *testing.T) {
	c := newTranscriptCache(8, time.Hour)
	key := transcriptKey{path: "/a"}
	a := c.acquire(key, newFakeStream)
	c.release(a)
	if c.acquire(key, newFakeStream) != a {
		t.Fatal("a released item should be reused within the idle window")
	}
}

func TestTranscriptCacheIdleLimitEvictsOldest(t *testing.T) {
	c := newTranscriptCache(2, time.Hour)
	k1, k2, k3 := transcriptKey{path: "/1"}, transcriptKey{path: "/2"}, transcriptKey{path: "/3"}
	live := c.acquire(transcriptKey{path: "/live"}, newFakeStream)
	i1 := c.acquire(k1, newFakeStream)
	i2 := c.acquire(k2, newFakeStream)
	i3 := c.acquire(k3, newFakeStream)
	c.release(i1)
	c.release(i2)
	if !c.has(k1) || !c.has(k2) {
		t.Fatal("idle items within the limit should stay")
	}
	c.release(i3)
	if c.has(k1) {
		t.Error("the longest-idle item should be evicted past the limit")
	}
	if !c.has(k2) || !c.has(k3) {
		t.Error("newer idle items should stay")
	}
	if !c.has(transcriptKey{path: "/live"}) {
		t.Error("an item with a subscriber should never be evicted")
	}
	c.release(live)
}

func TestTranscriptCacheIdleTimeoutEvicts(t *testing.T) {
	c := newTranscriptCache(8, 20*time.Millisecond)
	key := transcriptKey{path: "/a"}
	c.release(c.acquire(key, newFakeStream))
	waitTranscriptEvicted(t, c, key)
}

func TestTranscriptCacheAcquireStopsIdleTimeout(t *testing.T) {
	c := newTranscriptCache(8, 20*time.Millisecond)
	key := transcriptKey{path: "/a"}
	a := c.acquire(key, newFakeStream)
	c.release(a)
	c.acquire(key, newFakeStream)
	time.Sleep(60 * time.Millisecond)
	if !c.has(key) {
		t.Fatal("an item with a subscriber should outlive the idle timeout")
	}
}

func TestTranscriptCacheReleaseRestartsTimeout(t *testing.T) {
	c := newTranscriptCache(8, 40*time.Millisecond)
	key := transcriptKey{path: "/a"}
	a := c.acquire(key, newFakeStream)
	c.release(a)
	time.Sleep(25 * time.Millisecond)
	c.acquire(key, newFakeStream)
	c.release(a)
	time.Sleep(25 * time.Millisecond)
	if !c.has(key) {
		t.Fatal("the first release's timer should not evict after a later release")
	}
	waitTranscriptEvicted(t, c, key)
}

func TestSubscribeReleasesSharedTranscript(t *testing.T) {
	d := newNode(nil)
	tmp := writeTempTranscript(t)
	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		AgentSessionID: "node1",
		TranscriptPath: tmp,
		Status:         session.StatusIdle,
	})
	key := transcriptKey{path: tmp}
	refs := func() int {
		d.transcripts.mu.Lock()
		defer d.transcripts.mu.Unlock()
		if it, ok := d.transcripts.items[key]; ok {
			return it.refs
		}
		return -1
	}

	subscribe := func(subID string) context.CancelFunc {
		ctx, cancel := context.WithCancel(context.Background())
		ctx = api.WithNotifier(ctx, &fakeNotifier{ch: make(chan api.Notification, 8)})
		if _, err := d.handleTranscriptSubscribe(ctx, mustJSON(api.TranscriptSubscribeParams{SubID: subID, SessionID: s.ID})); err != nil {
			t.Fatal(err)
		}
		return cancel
	}
	waitRefs := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for refs() != want {
			if time.Now().After(deadline) {
				t.Fatalf("refs = %d, want %d", refs(), want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	cancelA := subscribe("a")
	cancelB := subscribe("b")
	waitRefs(2)
	cancelA()
	waitRefs(1)
	cancelB()
	waitRefs(0)
}
