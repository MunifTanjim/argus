package node

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

type countingSpawner struct {
	*fakeSpawner
	n    atomic.Int32
	gate <-chan struct{}
}

func (c *countingSpawner) SpawnSession(ctx context.Context, cwd string, opts adapter.SpawnOptions) (string, error) {
	c.n.Add(1)
	if c.gate != nil {
		<-c.gate
	}
	return c.fakeSpawner.SpawnSession(ctx, cwd, opts)
}

// countSpawns counts the fake agent's spawns; each spawn waits for gate when it is non-nil.
func countSpawns(d *Node, gate <-chan struct{}) *atomic.Int32 {
	c := &countingSpawner{fakeSpawner: d.adapters["fake"].(*fakeSpawner), gate: gate}
	d.adapters["fake"] = c
	return &c.n
}

func spawnRequest(t *testing.T, d *Node, p api.OrchSpawnParams) (api.OrchSpawnResult, error) {
	t.Helper()
	params, _ := json.Marshal(p)
	out, err := d.handleOrchSessionSpawn(t.Context(), params)
	if err != nil {
		return api.OrchSpawnResult{}, err
	}
	return out.(api.OrchSpawnResult), nil
}

func TestAgentSpawnRequestIDReturnsFirstResult(t *testing.T) {
	d, _, caller := spawnNode(t, true)
	n := countSpawns(d, nil)
	p := api.OrchSpawnParams{Caller: caller, Agent: "fake", Cwd: t.TempDir(), Prompt: "build it", RequestID: "r1"}
	first, err := spawnRequest(t, d, p)
	if err != nil {
		t.Fatal(err)
	}
	again, err := spawnRequest(t, d, p)
	if err != nil {
		t.Fatal(err)
	}
	if again != first || n.Load() != 1 {
		t.Fatalf("first=%+v again=%+v spawns=%d", first, again, n.Load())
	}
}

func TestAgentSpawnRequestIDReplayKeepsSessionID(t *testing.T) {
	d, _, caller := spawnNode(t, true)
	p := api.OrchSpawnParams{Caller: caller, Agent: "fake", Cwd: t.TempDir(), Prompt: "build it", RequestID: "r1"}
	first, err := spawnRequest(t, d, p)
	if err != nil {
		t.Fatal(err)
	}
	links, err := d.orch.Links(t.Context())
	if err != nil || len(links) != 1 || links[0].ChildSessionID != first.SessionID {
		t.Fatalf("links = %+v err=%v, want child_session_id %s", links, err, first.SessionID)
	}
	again, err := spawnRequest(t, d, p)
	if err != nil || again.SessionID != first.SessionID {
		t.Fatalf("replay = %+v err=%v, want session %s", again, err, first.SessionID)
	}
}

func TestAgentSpawnRequestIDWithDifferentParamsFails(t *testing.T) {
	d, _, caller := spawnNode(t, true)
	n := countSpawns(d, nil)
	p := api.OrchSpawnParams{Caller: caller, Agent: "fake", Cwd: t.TempDir(), Prompt: "build it", RequestID: "r1"}
	if _, err := spawnRequest(t, d, p); err != nil {
		t.Fatal(err)
	}
	p.Prompt = "test it"
	if _, err := spawnRequest(t, d, p); err == nil || !strings.Contains(err.Error(), "different parameters") {
		t.Fatalf("err = %v", err)
	}
	if n.Load() != 1 {
		t.Fatalf("spawns = %d", n.Load())
	}
}

func TestAgentSpawnRequestIDRetriesAfterFailure(t *testing.T) {
	d, fs, caller := spawnNode(t, true)
	n := countSpawns(d, nil)
	fs.err = errors.New("boom")
	p := api.OrchSpawnParams{Caller: caller, Agent: "fake", Cwd: t.TempDir(), RequestID: "r1"}
	if _, err := spawnRequest(t, d, p); err == nil {
		t.Fatal("expected error")
	}
	fs.err = nil
	res, err := spawnRequest(t, d, p)
	if err != nil || res.SessionID != "home:fake:ses_x" || n.Load() != 2 {
		t.Fatalf("res=%+v err=%v spawns=%d", res, err, n.Load())
	}
}

func TestAgentSpawnRequestIDConcurrentCallsSpawnOnce(t *testing.T) {
	d, _, caller := spawnNode(t, true)
	gate := make(chan struct{})
	n := countSpawns(d, gate)
	p := api.OrchSpawnParams{Caller: caller, Agent: "fake", Cwd: t.TempDir(), Prompt: "build it", RequestID: "r1"}
	var wg sync.WaitGroup
	results := make([]api.OrchSpawnResult, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Go(func() { results[i], errs[i] = spawnRequest(t, d, p) })
	}
	for n.Load() == 0 {
		runtime.Gosched()
	}
	close(gate)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || results[0] != results[1] || n.Load() != 1 {
		t.Fatalf("results=%+v errs=%v spawns=%d", results, errs, n.Load())
	}
}

func TestSendRequestIDReturnsFirstMessage(t *testing.T) {
	d, _ := deliverNode(t)
	a := apiSession(d, "a", session.StatusWorking, nil)
	b := apiSession(d, "b", session.StatusWorking, nil)
	first, err := d.send(t.Context(), d.localCallerRef(a), b.ID, "q", "r1")
	if err != nil {
		t.Fatal(err)
	}
	again, err := d.send(t.Context(), d.localCallerRef(a), b.ID, "q", "r1")
	if err != nil || again != first {
		t.Fatalf("first=%+v again=%+v err=%v", first, again, err)
	}
	if msgs, err := d.orch.Queued(t.Context(), api.Actor{Node: d.id, Key: "opencode:b"}); err != nil || len(msgs) != 1 {
		t.Fatalf("queued = %+v err=%v", msgs, err)
	}
}

func TestSendRequestIDWithDifferentTextFails(t *testing.T) {
	d, _ := deliverNode(t)
	a := apiSession(d, "a", session.StatusWorking, nil)
	b := apiSession(d, "b", session.StatusWorking, nil)
	if _, err := d.send(t.Context(), d.localCallerRef(a), b.ID, "q", "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.send(t.Context(), d.localCallerRef(a), b.ID, "other", "r1"); err == nil || !strings.Contains(err.Error(), "different parameters") {
		t.Fatalf("err = %v", err)
	}
}

func TestSendRequestIDIsScopedToSender(t *testing.T) {
	d, _ := deliverNode(t)
	a := apiSession(d, "a", session.StatusWorking, nil)
	c := apiSession(d, "c", session.StatusWorking, nil)
	b := apiSession(d, "b", session.StatusWorking, nil)
	m1, err := d.send(t.Context(), d.localCallerRef(a), b.ID, "q", "r1")
	if err != nil {
		t.Fatal(err)
	}
	m2, err := d.send(t.Context(), d.localCallerRef(c), b.ID, "q", "r1")
	if err != nil || m2 == m1 {
		t.Fatalf("m1=%+v m2=%+v err=%v", m1, m2, err)
	}
}
