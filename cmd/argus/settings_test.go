package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/tui"
)

func serveFakeGateway(t *testing.T, handlers map[string]api.HandlerFunc) string {
	t.Helper()
	srv := api.NewServer()
	for method, h := range handlers {
		srv.Handle(method, h)
	}
	ts := httptest.NewServer(srv.WSHandler(func(string) bool { return true }))
	t.Cleanup(ts.Close)
	return wsURL(ts.URL)
}

func returning(v any) api.HandlerFunc {
	return func(context.Context, json.RawMessage) (any, error) { return v, nil }
}

func closedPortURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return "ws://" + addr
}

func TestSettingsBackendLockStatusWithNoNode(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{Socket: shortSocket(t)}

	v, err := newSettingsBackend(context.Background(), cfg, "", "", nil).LockStatus(context.Background())

	if err != nil {
		t.Fatalf("LockStatus: %v", err)
	}
	if v.NodeRunning {
		t.Error("NodeRunning must be false with no socket")
	}
	for _, want := range []string{"this tui", "identity: devpub:"} {
		if !strings.Contains(v.TUIText, want) {
			t.Errorf("TUIText must contain %q, got:\n%s", want, v.TUIText)
		}
	}
}

func TestSettingsBackendJoinsTheRoster(t *testing.T) {
	tempStateDir(t)
	a, b := testGenesis(0xA1), testGenesis(0xB2)
	sock := serveFakeNode(t, map[string]api.HandlerFunc{
		api.MethodLockStatus: returning(api.LockStatusResult{Enabled: true, Devices: [][]byte{a}, DeviceCount: 1, IdentityPubKey: a}),
	})
	ep := serveFakeGateway(t, map[string]api.HandlerFunc{
		api.MethodNodesList: returning(api.NodesListResult{Nodes: []api.NodeDescriptor{
			{ID: "n1", Label: "home", IdentityPubKey: base64.StdEncoding.EncodeToString(a)},
			{ID: "n2", Label: "work", IdentityPubKey: base64.StdEncoding.EncodeToString(b)},
		}}),
	})
	cfg := &config.Config{Socket: sock}

	v, err := newSettingsBackend(context.Background(), cfg, ep, "", nil).LockStatus(context.Background())

	if err != nil {
		t.Fatalf("LockStatus: %v", err)
	}
	if v.RosterErr != "" {
		t.Fatalf("RosterErr = %q", v.RosterErr)
	}
	if v.Summary.Devices != tui.DevicesSomeUnsigned || v.Summary.Unsigned != 1 {
		t.Errorf("Summary = %+v, want DevicesSomeUnsigned with 1 unsigned", v.Summary)
	}
	want := []tui.LockDevice{
		{Key: a, Label: "home", Authorized: true, ThisNode: true},
		{Key: b, Label: "work", Authorized: false},
	}
	if len(v.Devices) != len(want) {
		t.Fatalf("Devices = %+v, want %+v", v.Devices, want)
	}
	for i, d := range v.Devices {
		w := want[i]
		if !bytes.Equal(d.Key, w.Key) || d.Label != w.Label || d.Authorized != w.Authorized || d.ThisNode != w.ThisNode {
			t.Errorf("Devices[%d] = %+v, want %+v", i, d, w)
		}
	}
}

func TestSettingsBackendReportsARosterErrorWithoutFailing(t *testing.T) {
	tempStateDir(t)
	a := testGenesis(0xA1)
	sock := serveFakeNode(t, map[string]api.HandlerFunc{
		api.MethodLockStatus: returning(api.LockStatusResult{Enabled: true, Devices: [][]byte{a}, DeviceCount: 1}),
	})
	cfg := &config.Config{Socket: sock}

	v, err := newSettingsBackend(context.Background(), cfg, closedPortURL(t), "", nil).LockStatus(context.Background())

	if err != nil {
		t.Fatalf("LockStatus: %v", err)
	}
	if v.RosterErr == "" {
		t.Error("RosterErr must name the roster failure")
	}
	if v.Summary.Devices != tui.DevicesLabelsFailed {
		t.Errorf("Devices = %v, want DevicesLabelsFailed", v.Summary.Devices)
	}
	if len(v.Devices) != 1 || !bytes.Equal(v.Devices[0].Key, a) || v.Devices[0].Label != "" || !v.Devices[0].Authorized {
		t.Errorf("Devices = %+v, want the authorized key without a label", v.Devices)
	}
}

func TestSettingsBackendLockLogMatchesTheCLI(t *testing.T) {
	tempStateDir(t)
	res := api.LockLogResult{
		Entries: []api.LockLogEntry{
			{Index: 0, Kind: "genesis", Signers: [][]byte{testGenesis(0xF1)}, Hash: testGenesis(0xE0)},
			{Index: 1, Kind: "authorize-device", Target: testGenesis(0xD1), Hash: testGenesis(0xE1)},
			{Index: 2, Kind: "revoke-signer", Revoked: [][]byte{testGenesis(0xF2)}, CoSignCount: 2, Hash: testGenesis(0xE2)},
		},
		Tip:     testGenesis(0xE2),
		Signers: [][]byte{testGenesis(0xF1)},
	}
	sock := serveFakeNode(t, map[string]api.HandlerFunc{api.MethodLockLog: returning(res)})
	cfg := &config.Config{Socket: sock}

	got, err := newSettingsBackend(context.Background(), cfg, "", "", nil).LockLog(context.Background())
	if err != nil {
		t.Fatalf("LockLog: %v", err)
	}
	printed := captureStdout(t, func() {
		root := newRootCmd("test")
		root.SetArgs([]string{"lock", "log", "--no-config", "--socket", sock})
		if err := root.Execute(); err != nil {
			t.Errorf("lock log: %v", err)
		}
	})

	if got != printed {
		t.Errorf("LockLog = %q, argus lock log printed %q", got, printed)
	}
}

func TestSettingsBackendStartPairingNeedsAPublicURL(t *testing.T) {
	ep := serveFakeGateway(t, map[string]api.HandlerFunc{
		api.MethodClientsPairStart: returning(api.PairStartResult{Token: "t"}),
	})

	_, err := newSettingsBackend(context.Background(), &config.Config{}, ep, "", nil).StartPairing(context.Background())

	if err == nil || !strings.Contains(err.Error(), "no public URL") {
		t.Fatalf("err = %v, want one naming the missing public URL", err)
	}
}

func TestSettingsBackendStartPairingBuildsTheURI(t *testing.T) {
	ep := serveFakeGateway(t, map[string]api.HandlerFunc{
		api.MethodClientsPairStart: returning(api.PairStartResult{Token: "t", URL: "https://h.example"}),
	})

	offer, err := newSettingsBackend(context.Background(), &config.Config{}, ep, "", nil).StartPairing(context.Background())

	if err != nil {
		t.Fatalf("StartPairing: %v", err)
	}
	if want := "argus://pair?token=t&url=wss%3A%2F%2Fh.example"; offer.URI != want {
		t.Errorf("URI = %q, want %q", offer.URI, want)
	}
	if offer.Token != "t" {
		t.Errorf("Token = %q, want t", offer.Token)
	}
	if offer.URL != "wss://h.example" {
		t.Errorf("URL = %q, want wss://h.example", offer.URL)
	}
}

func TestSettingsBackendAwaitPairingStopsOnCancel(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	ep := serveFakeGateway(t, map[string]api.HandlerFunc{
		api.MethodClientsPairAwait: func(ctx context.Context, _ json.RawMessage) (any, error) {
			select {
			case <-ctx.Done():
			case <-release:
			}
			return api.PairAwaitResult{}, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	_, err := newSettingsBackend(context.Background(), &config.Config{}, ep, "", nil).AwaitPairing(ctx, "t")

	if err == nil {
		t.Error("a cancelled await must return an error")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("AwaitPairing took %s after cancel", d)
	}
}

func TestSettingsBackendWritesNothing(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{Socket: shortSocket(t)}
	b := newSettingsBackend(context.Background(), cfg, "", "", nil)

	stdout, stderr := captureOutput(t, func() {
		if _, err := b.LockStatus(context.Background()); err != nil {
			t.Errorf("LockStatus: %v", err)
		}
		if _, err := b.Pin(context.Background(), testGenesis(0x66)); err != nil {
			t.Errorf("Pin: %v", err)
		}
		if _, err := b.Unpin(context.Background()); err != nil {
			t.Errorf("Unpin: %v", err)
		}
	})

	if stdout != "" || stderr != "" {
		t.Errorf("backend wrote stdout %q, stderr %q", stdout, stderr)
	}
}

func blockingHandler(t *testing.T) api.HandlerFunc {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		select {
		case <-ctx.Done():
		case <-release:
		}
		return nil, nil
	}
}

func TestSettingsBackendBoundsARosterThatNeverAnswers(t *testing.T) {
	tempStateDir(t)
	restore := gatewayProbeTimeout
	gatewayProbeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { gatewayProbeTimeout = restore })
	a := testGenesis(0xA1)
	sock := serveFakeNode(t, map[string]api.HandlerFunc{
		api.MethodLockStatus: returning(api.LockStatusResult{Enabled: true, Devices: [][]byte{a}, DeviceCount: 1}),
	})
	ep := serveFakeGateway(t, map[string]api.HandlerFunc{api.MethodNodesList: blockingHandler(t)})
	cfg := &config.Config{Socket: sock}

	start := time.Now()
	v, err := newSettingsBackend(context.Background(), cfg, ep, "", nil).LockStatus(context.Background())

	if err != nil {
		t.Fatalf("LockStatus: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("LockStatus took %s on a roster that never answers", d)
	}
	if !strings.Contains(v.RosterErr, "did not answer") {
		t.Errorf("RosterErr = %q, want the unanswered roster named", v.RosterErr)
	}
}

func TestSettingsBackendPairedDevicesStopsAtTheDeadline(t *testing.T) {
	ep := serveFakeGateway(t, map[string]api.HandlerFunc{api.MethodClientsList: blockingHandler(t)})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := newSettingsBackend(context.Background(), &config.Config{}, ep, "", nil).PairedDevices(ctx)

	if err == nil {
		t.Error("a call past its deadline must return an error")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("PairedDevices took %s past its deadline", d)
	}
}

func TestSettingsBackendReconnectReturnsAClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := &config.Config{Socket: serveFakeNode(t, nil)}

	c, err := newSettingsBackend(ctx, cfg, "", "", nil).Reconnect()

	if err != nil || c == nil {
		t.Fatalf("Reconnect = %v, %v; want a client", c, err)
	}
	c.Close()
}

func TestSettingsBackendReconnectFailsOnABadEndpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := &config.Config{Socket: shortSocket(t)}

	c, err := newSettingsBackend(ctx, cfg, closedPortURL(t), "", nil).Reconnect()

	if err == nil || c != nil {
		t.Fatalf("Reconnect = %v, %v; want an error and no client", c, err)
	}
}

func TestCallLocalStopsAtTheDeadlineOnAHungNode(t *testing.T) {
	cfg := &config.Config{Socket: serveFakeNode(t, map[string]api.HandlerFunc{api.MethodLockStatus: blockingHandler(t)})}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := lockStatusOnNode(ctx, cfg)

	if err == nil {
		t.Error("a call past its deadline must return an error")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("lockStatusOnNode took %s past its deadline", d)
	}
}

func TestSettingsBackendLockStatusWithAnUnreadableIdentity(t *testing.T) {
	dir := tempStateDir(t)
	if err := os.WriteFile(filepath.Join(dir, "client-identity.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := testGenesis(0xA1)
	sock := serveFakeNode(t, map[string]api.HandlerFunc{
		api.MethodLockStatus: returning(api.LockStatusResult{Enabled: true, Devices: [][]byte{a}, DeviceCount: 1}),
	})

	v, err := newSettingsBackend(context.Background(), &config.Config{Socket: sock}, "", "", nil).LockStatus(context.Background())

	if err != nil {
		t.Fatalf("LockStatus: %v", err)
	}
	if !v.NodeRunning || v.StatusText == "" {
		t.Errorf("the node status is missing: %+v", v)
	}
	if v.ClientIdentity != nil {
		t.Errorf("ClientIdentity = %x, want none", v.ClientIdentity)
	}
}

func TestSettingsBackendLockStatusOnAStaleSocketKeepsTheClientBlock(t *testing.T) {
	tempStateDir(t)
	sock := shortSocket(t)
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()

	v, err := newSettingsBackend(context.Background(), &config.Config{Socket: sock}, "", "", nil).LockStatus(context.Background())

	if err != nil {
		t.Errorf("err = %v, want the row to carry the error", err)
	}
	if v.Summary.Lock != tui.LockNodeError || !strings.Contains(v.Summary.NodeErr, "node socket") {
		t.Errorf("Summary = %+v, want the node socket error", v.Summary)
	}
	if !strings.Contains(v.TUIText, "no local node") || !strings.Contains(v.TUIText, "this tui") {
		t.Errorf("TUIText lacks the client block:\n%s", v.TUIText)
	}
	if len(v.ClientIdentity) == 0 {
		t.Error("ClientIdentity is missing")
	}
}

func TestSettingsBackendPinSurvivesAFailedReconnect(t *testing.T) {
	tempStateDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := &config.Config{Socket: serveFakeNode(t, map[string]api.HandlerFunc{api.MethodLockPin: acceptPin})}
	cfg.E2EE.Enabled = true
	genesis := testGenesis(0x77)
	b := newSettingsBackend(ctx, cfg, closedPortURL(t), "", nil)

	if _, err := b.Pin(ctx, genesis); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if c, err := b.Reconnect(); err == nil {
		c.Close()
		t.Fatal("Reconnect to a closed port must fail")
	}

	pin, err := clientPinFile().Load()
	if err != nil || !bytes.Equal(pin, genesis) {
		t.Fatalf("client pin = %x (%v), want %x", pin, err, genesis)
	}
}

type closeCountClient struct {
	tui.Client
	closed chan struct{}
}

func (c closeCountClient) Close() error { close(c.closed); return nil }

func TestSettingsBackendReconnectGivesUpOnAConnectThatNeverReturns(t *testing.T) {
	b := newSettingsBackend(t.Context(), &config.Config{}, "", "", nil)
	b.reconnectTimeout = 100 * time.Millisecond
	b.dial = func(ctx context.Context, _ []byte) (tui.Client, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	start := time.Now()
	c, err := b.Reconnect()

	if err == nil || c != nil {
		t.Fatalf("Reconnect = %v, %v; want an error and no client", c, err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("Reconnect took %s past its bound", d)
	}
}

func TestSettingsBackendReconnectClosesAClientThatArrivesLate(t *testing.T) {
	b := newSettingsBackend(t.Context(), &config.Config{}, "", "", nil)
	b.reconnectTimeout = 50 * time.Millisecond
	late := closeCountClient{closed: make(chan struct{})}
	b.dial = func(context.Context, []byte) (tui.Client, error) {
		time.Sleep(150 * time.Millisecond)
		return late, nil
	}

	if _, err := b.Reconnect(); err == nil {
		t.Fatal("Reconnect must give up before the client arrives")
	}

	select {
	case <-late.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the late client was never closed")
	}
}

// testGatewayURL is never dialed: it only puts the backend in gateway mode.
const testGatewayURL = "ws://gateway.invalid"

func e2eeConfig(sock string) *config.Config {
	cfg := &config.Config{Socket: sock}
	cfg.E2EE.Enabled = true
	return cfg
}

func TestSettingsBackendPinReportsAChangedClientPin(t *testing.T) {
	tempStateDir(t)
	b := newSettingsBackend(t.Context(), e2eeConfig(serveFakeNode(t, map[string]api.HandlerFunc{api.MethodLockPin: acceptPin})), testGatewayURL, "", nil)

	res, err := b.Pin(t.Context(), testGenesis(0x81))

	if err != nil || !res.ClientPinChanged {
		t.Fatalf("Pin = %+v, %v; want the client pin changed", res, err)
	}
}

func TestSettingsBackendLocalSocketPinNeedsNoRebuild(t *testing.T) {
	tempStateDir(t)
	b := newSettingsBackend(t.Context(), e2eeConfig(serveFakeNode(t, map[string]api.HandlerFunc{api.MethodLockPin: acceptPin})), "", "", nil)

	res, err := b.Pin(t.Context(), testGenesis(0x85))

	if err != nil || res.ClientPinChanged {
		t.Fatalf("Pin = %+v, %v; want no client rebuild over the local socket", res, err)
	}
}

func TestSettingsBackendRefusedPinLeavesTheClientPin(t *testing.T) {
	tempStateDir(t)
	b := newSettingsBackend(t.Context(), e2eeConfig(serveFakeNode(t, map[string]api.HandlerFunc{api.MethodLockPin: refusePin})), testGatewayURL, "", nil)

	res, err := b.Pin(t.Context(), testGenesis(0x82))

	if err == nil || res.ClientPinChanged {
		t.Fatalf("Pin = %+v, %v; want an error and the client pin unchanged", res, err)
	}
}

func TestSettingsBackendPartialUnpinReportsAChangedClientPin(t *testing.T) {
	dir := tempStateDir(t)
	g := testGenesis(0x83)
	if err := clientPinFile().Save(g); err != nil {
		t.Fatal(err)
	}
	blockRemoval(t, dir, "client-trustlog-chain")
	b := newSettingsBackend(t.Context(), e2eeConfig(shortSocket(t)), testGatewayURL, "", g)

	res, err := b.Unpin(t.Context())

	if err == nil || !res.ClientPinChanged {
		t.Fatalf("Unpin = %+v, %v; want an error and the client pin changed", res, err)
	}
}

func TestSettingsBackendReconnectTracksTheNewClientPin(t *testing.T) {
	tempStateDir(t)
	b := newSettingsBackend(t.Context(), e2eeConfig(serveFakeNode(t, map[string]api.HandlerFunc{api.MethodLockPin: acceptPin})), testGatewayURL, "", nil)
	b.dial = func(context.Context, []byte) (tui.Client, error) {
		return closeCountClient{closed: make(chan struct{})}, nil
	}
	g := testGenesis(0x84)
	if _, err := b.Pin(t.Context(), g); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if _, err := b.Reconnect(); err != nil {
		t.Fatalf("Reconnect: %v", err)
	}

	res, err := b.Pin(t.Context(), g)

	if err != nil || res.ClientPinChanged {
		t.Fatalf("Pin = %+v, %v; want the client pin unchanged after the rebuild", res, err)
	}
}

func summaryOf(t *testing.T, sock string) tui.LockSummary {
	t.Helper()
	v, err := newSettingsBackend(context.Background(), &config.Config{Socket: sock}, "", "", nil).LockStatus(context.Background())
	if err != nil {
		t.Fatalf("LockStatus: %v", err)
	}
	return v.Summary
}

func TestSettingsSummaryEnforcing(t *testing.T) {
	tempStateDir(t)
	a, root := testGenesis(0xA1), testGenesis(0xC1)
	sock := serveFakeNode(t, map[string]api.HandlerFunc{
		api.MethodLockStatus: returning(api.LockStatusResult{
			Enabled: true, Authorized: true, SignerTrusted: true, Pinned: true, PinGenesis: root,
			Devices: [][]byte{a}, DeviceCount: 1, IdentityPubKey: a,
		}),
	})

	s := summaryOf(t, sock)

	if s.Lock != tui.LockEnforcing || s.TrustRoot != fingerprintOf(root) || s.Node != tui.NodeSigner || s.Devices != tui.DevicesListed {
		t.Errorf("Summary = %+v", s)
	}
}

func TestSettingsSummaryNeverBetterThanTheCLI(t *testing.T) {
	for _, c := range lockCaseFixtures {
		if !strings.Contains(c.headline, "locked out") && !strings.Contains(c.headline, "disabled") {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			tempStateDir(t)
			sock := serveFakeNode(t, map[string]api.HandlerFunc{api.MethodLockStatus: returning(c.st)})
			if got := summaryOf(t, sock).Lock.Tone(); got != tui.ToneBad {
				t.Errorf("Tone = %v, want bad", got)
			}
		})
	}
}

func TestSettingsSummaryNoLocalNodeWithUnreadableIdentity(t *testing.T) {
	dir := tempStateDir(t)
	if err := os.Mkdir(filepath.Join(dir, "client-identity.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	v, err := newSettingsBackend(context.Background(), &config.Config{Socket: shortSocket(t)}, "", "", nil).LockStatus(context.Background())

	if err != nil {
		t.Fatalf("LockStatus: %v", err)
	}
	if v.Summary.Lock != tui.LockNoNode || v.Summary.TUI != tui.TUIIdentityUnreadable || v.Summary.TUIErr == "" {
		t.Errorf("Summary = %+v", v.Summary)
	}
	if !strings.HasPrefix(v.TUIText, "  pin:") {
		t.Errorf("TUIText = %q, want the pin line alone", v.TUIText)
	}
}

func TestSettingsSummaryOldDaemon(t *testing.T) {
	tempStateDir(t)
	sock := serveFakeNode(t, map[string]api.HandlerFunc{
		api.MethodLockStatus: returning(api.LockStatusResult{Enabled: true, DeviceCount: 2}),
	})

	s := summaryOf(t, sock)

	if s.Devices != tui.DevicesUnreported || s.TUISign != tui.TUISignUnknown || s.CanSignTUI {
		t.Errorf("Summary = %+v", s)
	}
}

func TestSettingsSummaryTUIUnsigned(t *testing.T) {
	tempStateDir(t)
	other := testGenesis(0xA1)
	sock := serveFakeNode(t, map[string]api.HandlerFunc{
		api.MethodLockStatus: returning(api.LockStatusResult{
			Enabled: true, Authorized: true, SignerTrusted: true,
			Devices: [][]byte{other}, DeviceCount: 1, IdentityPubKey: other,
		}),
	})
	kp, err := e2e.LoadOrCreateIdentity(config.GetStatePath("client-identity.json"))
	if err != nil {
		t.Fatal(err)
	}

	s := summaryOf(t, sock)

	if s.TUISign != tui.TUIUnsigned || !s.CanSignTUI || s.TUISignFix != lockSignHint(kp.Public) {
		t.Errorf("Summary = %+v", s)
	}
}
