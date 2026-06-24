package main

import (
	"context"
	"errors"
	"net/http/httptest"
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
