package main

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/gateway"
)

// TestPingRoundTripsOverGateway confirms the gateway's client server answers the ping RPC, so a
// real latency probe completes end-to-end.
func TestPingRoundTripsOverGateway(t *testing.T) {
	hsrv := gateway.NewServer(gateway.New(0), nil, nil) // allow all
	ts := httptest.NewServer(hsrv.Handler())
	defer ts.Close()

	c, err := api.DialWS(context.Background(), wsURL(ts.URL)+"/client", "", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	if err := c.Call(api.MethodPing, nil, nil); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

// fakePinger counts calls and returns a scripted error.
type fakePinger struct {
	calls int
	err   error
}

func (f *fakePinger) Call(string, any, any) error {
	f.calls++
	return f.err
}

func TestRunPingsCollectsSamples(t *testing.T) {
	f := &fakePinger{}
	var reported int
	rtts := runPings(context.Background(), f, 3, 0, func(int, time.Duration, error) { reported++ })
	if f.calls != 3 || reported != 3 || len(rtts) != 3 {
		t.Fatalf("calls=%d reported=%d samples=%d, want 3/3/3", f.calls, reported, len(rtts))
	}
	for _, r := range rtts {
		if r < 0 {
			t.Errorf("negative rtt %v", r)
		}
	}
}

func TestRunPingsRecordsFailures(t *testing.T) {
	f := &fakePinger{err: errors.New("down")}
	rtts := runPings(context.Background(), f, 3, 0, func(int, time.Duration, error) {})
	if len(rtts) != 0 {
		t.Fatalf("want 0 successful samples on persistent failure, got %d", len(rtts))
	}
}

func TestRunPingsStopsOnCancel(t *testing.T) {
	f := &fakePinger{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	// count 5 with a long interval: after the first ping the cancelled ctx halts the loop.
	rtts := runPings(ctx, f, 5, time.Hour, func(int, time.Duration, error) {})
	if len(rtts) != 1 {
		t.Fatalf("want 1 sample before cancel halts the loop, got %d", len(rtts))
	}
}

// fakeFleet is a gateway client with several online nodes whose links settle one by one.
type fakeFleet struct {
	mu      sync.Mutex
	roster  []api.NodeDescriptor
	vias    map[string]string
	failing int // nodes.list calls left to fail
}

func (f *fakeFleet) Call(method string, _, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failing > 0 {
		f.failing--
		return errors.New("gateway busy")
	}
	*(out.(*api.NodesListResult)) = api.NodesListResult{Nodes: f.roster}
	return nil
}

func (f *fakeFleet) NodeVias() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.vias)
}

func (f *fakeFleet) set(id, via string) {
	f.mu.Lock()
	f.vias[id] = via
	f.mu.Unlock()
}

func newFakeFleet() *fakeFleet {
	return &fakeFleet{
		roster: []api.NodeDescriptor{{ID: "c", Online: true}, {ID: "a", Online: true}, {ID: "b", Online: true}, {ID: "off"}},
		vias:   map[string]string{},
	}
}

func TestAwaitPingNodesWaitsForEveryOnlineNode(t *testing.T) {
	f := newFakeFleet()
	f.set("a", "gateway")
	go func() {
		time.Sleep(150 * time.Millisecond)
		f.set("b", "tcp")
		time.Sleep(150 * time.Millisecond)
		f.set("c", "ssh")
	}()
	nodes, err := awaitPingNodes(context.Background(), f, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := []pingNode{{"a", "gateway"}, {"b", "tcp"}, {"c", "ssh"}}
	if !slices.Equal(nodes, want) {
		t.Fatalf("nodes = %+v, want %+v", nodes, want)
	}
	b, _ := json.Marshal(pingResult{Nodes: nodes})
	if !strings.Contains(string(b), `"via":"gateway"`) {
		t.Errorf("%s lacks via gateway", b)
	}
}

func TestAwaitPingNodesListsUnsettledAsUnknown(t *testing.T) {
	f := newFakeFleet()
	f.set("a", "gateway")
	nodes, err := awaitPingNodes(context.Background(), f, 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	want := []pingNode{{"a", "gateway"}, {"b", pingUnknownVia}, {"c", pingUnknownVia}}
	if !slices.Equal(nodes, want) {
		t.Fatalf("nodes = %+v, want %+v", nodes, want)
	}
}

// A roster call that fails is retried; one that still fails at the deadline
// is reported, not taken for an empty roster.
func TestAwaitPingNodesRosterError(t *testing.T) {
	f := newFakeFleet()
	f.set("a", "gateway")
	f.set("b", "tcp")
	f.set("c", "ssh")
	f.failing = 2
	nodes, err := awaitPingNodes(context.Background(), f, 5*time.Second)
	if err != nil {
		t.Fatalf("err = %v after a recovered roster", err)
	}
	if want := []pingNode{{"a", "gateway"}, {"b", "tcp"}, {"c", "ssh"}}; !slices.Equal(nodes, want) {
		t.Fatalf("nodes = %+v, want %+v", nodes, want)
	}

	f.failing = 1 << 30
	if _, err := awaitPingNodes(context.Background(), f, 250*time.Millisecond); err == nil {
		t.Fatal("a roster that never lists must be an error")
	}
}
