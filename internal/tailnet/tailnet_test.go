package tailnet

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

type fakeDaemon struct {
	notFound int // WhoIs calls that return not-found before succeeding
	err      error
	calls    int
	last     string
	status   *ipnstate.Status
}

func (f *fakeDaemon) WhoIs(ctx context.Context, addr string) (*apitype.WhoIsResponse, error) {
	f.calls++
	f.last = addr
	if f.err != nil {
		return nil, f.err
	}
	if f.calls <= f.notFound {
		return nil, local.ErrPeerNotFound
	}
	return &apitype.WhoIsResponse{Node: &tailcfg.Node{StableID: "n1"}}, nil
}

func (f *fakeDaemon) Status(ctx context.Context) (*ipnstate.Status, error) {
	if f.status == nil {
		return nil, errors.New("down")
	}
	return f.status, nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func running() *ipnstate.Status {
	return &ipnstate.Status{
		BackendState: "Running",
		Self: &ipnstate.PeerStatus{
			TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.1")},
			DNSName:      "box.tail.ts.net.",
		},
	}
}

func TestPeerRetriesNotFound(t *testing.T) {
	d := &fakeDaemon{notFound: 2}
	ok, err := Peer(context.Background(), d, "100.64.0.2:1234")
	if err != nil || !ok {
		t.Fatalf("got %v, %v", ok, err)
	}
	if d.calls != 3 {
		t.Fatalf("calls = %d", d.calls)
	}
}

func TestPeerGivesUp(t *testing.T) {
	d := &fakeDaemon{notFound: 1 << 30}
	start := time.Now()
	ok, err := Peer(context.Background(), d, "100.64.0.2:1234")
	if ok || err != nil {
		t.Fatalf("got %v, %v", ok, err)
	}
	if el := time.Since(start); el > 600*time.Millisecond {
		t.Fatalf("took %v", el)
	}
}

func TestPeerIdentity(t *testing.T) {
	id, err := PeerIdentity(context.Background(), &fakeDaemon{notFound: 1}, "100.64.0.2:1234")
	if err != nil || id != "n1" {
		t.Fatalf("got %q, %v; want n1", id, err)
	}
	id, err = PeerIdentity(context.Background(), &fakeDaemon{err: errors.New("boom")}, "100.64.0.2:1234")
	if err == nil || id != "" {
		t.Fatalf("daemon error: got %q, %v", id, err)
	}
}

func TestPeerOtherErrorFailsFast(t *testing.T) {
	d := &fakeDaemon{err: errors.New("boom")}
	ok, err := Peer(context.Background(), d, "100.64.0.2:1234")
	if ok || err == nil {
		t.Fatalf("got %v, %v", ok, err)
	}
	if d.calls != 1 {
		t.Fatalf("calls = %d", d.calls)
	}
}

func TestDialableRequiresRunning(t *testing.T) {
	st := running()
	d := &fakeDaemon{status: st}
	if !Dialable(context.Background(), d, "100.64.0.2") {
		t.Fatal("running peer should be dialable")
	}
	st.BackendState = "Stopped"
	if Dialable(context.Background(), d, "100.64.0.2") {
		t.Fatal("stopped backend should not be dialable")
	}
	d = &fakeDaemon{status: running(), notFound: 1 << 30}
	if Dialable(context.Background(), d, "8.8.8.8") {
		t.Fatal("non-peer should not be dialable")
	}
}

func TestAdvertisedKeepsOwnHosts(t *testing.T) {
	d := &fakeDaemon{status: running()}
	got := Advertised(context.Background(), d,
		[]string{"100.64.0.1:7777", "192.168.1.5:7777"}, nil, quiet())
	want := []string{"ws://100.64.0.1:7777/p2p"}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %v", got)
	}
	got = Advertised(context.Background(), d, nil,
		[]string{"ws://box.tail.ts.net:7777/p2p", "ws://other.example:1/p2p", "ws://100.64.0.1:1/p2p"}, quiet())
	if len(got) != 2 || got[0] != "ws://box.tail.ts.net:7777/p2p" || got[1] != "ws://100.64.0.1:1/p2p" {
		t.Fatalf("got %v", got)
	}
}

func TestAdvertisedLoopbackNeedsOverride(t *testing.T) {
	d := &fakeDaemon{status: running()}
	got := Advertised(context.Background(), d, []string{"127.0.0.1:7777"}, nil, quiet())
	if len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	got = Advertised(context.Background(), d, []string{"127.0.0.1:7777"}, []string{"ws://box.tail.ts.net:7777/p2p"}, quiet())
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestAdvertisedDaemonDown(t *testing.T) {
	if got := Advertised(context.Background(), &fakeDaemon{}, []string{"100.64.0.1:1"}, nil, quiet()); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func withPeer() *ipnstate.Status {
	st := running()
	st.Peer = map[key.NodePublic]*ipnstate.PeerStatus{
		key.NewNode().Public(): {DNSName: "peer.tail.ts.net.", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.9")}},
	}
	return st
}

func TestDialableResolvesNameViaPeers(t *testing.T) {
	d := &fakeDaemon{status: withPeer()}
	if !Dialable(context.Background(), d, "Peer.tail.ts.net") {
		t.Fatal("named peer should be dialable")
	}
	if d.last != "100.64.0.9" {
		t.Fatalf("WhoIs addr = %q", d.last)
	}
}

func TestDialableUnknownName(t *testing.T) {
	d := &fakeDaemon{status: withPeer()}
	if Dialable(context.Background(), d, "nobody.tail.ts.net") {
		t.Fatal("unknown name should not be dialable")
	}
	if d.calls != 0 {
		t.Fatalf("WhoIs calls = %d", d.calls)
	}
}

func TestDialableIPv6(t *testing.T) {
	d := &fakeDaemon{status: running()}
	if !Dialable(context.Background(), d, "fd7a:115c:a1e0::1") {
		t.Fatal("ipv6 peer should be dialable")
	}
	if d.last != "fd7a:115c:a1e0::1" {
		t.Fatalf("WhoIs addr = %q", d.last)
	}
}
