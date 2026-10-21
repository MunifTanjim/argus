package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/trustlog"
	"github.com/MunifTanjim/argus/internal/trustpin"
)

func tempStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := config.StateDir
	config.StateDir = dir
	t.Cleanup(func() { config.StateDir = old })
	return dir
}

// The socket lives in a short temp dir: macOS caps unix socket paths at ~104 bytes.
func serveFakeNode(t *testing.T, handlers map[string]api.HandlerFunc) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ap")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	sock := filepath.Join(dir, "node.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := api.NewServer()
	for method, h := range handlers {
		srv.Handle(method, h)
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() {
		l.Close()
		os.RemoveAll(dir)
	})
	return sock
}

func acceptPin(_ context.Context, _ json.RawMessage) (any, error) { return nil, nil }

func refusePin(_ context.Context, _ json.RawMessage) (any, error) {
	return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "node: already pinned to a different genesis; run `argus lock unpin` first"}
}

// TestApplyPinLeavesTheClientUnpinnedWhenTheNodeRefuses is the node=X client=Y trap:
// a client pinned to a genesis the node rejects opens zero channels forever, with no
// error and no quarantine to explain it.
func TestApplyPinLeavesTheClientUnpinnedWhenTheNodeRefuses(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{Socket: serveFakeNode(t, map[string]api.HandlerFunc{api.MethodLockPin: refusePin})}

	err := applyPin(context.Background(), cfg, testGenesis(0x22), io.Discard, io.Discard)

	if err == nil {
		t.Fatal("a node-side refusal must fail the command")
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Fatalf("error must say the node refused, got: %v", err)
	}
	pin, perr := clientPinFile().Load()
	if perr != nil || pin != nil {
		t.Fatalf("no client pin must be written: got %x, %v", pin, perr)
	}
}

func TestApplyPinWritesTheClientPinWhenTheNodeAccepts(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{Socket: serveFakeNode(t, map[string]api.HandlerFunc{api.MethodLockPin: acceptPin})}
	genesis := testGenesis(0x33)

	if err := applyPin(context.Background(), cfg, genesis, io.Discard, io.Discard); err != nil {
		t.Fatalf("applyPin: %v", err)
	}

	pin, err := clientPinFile().Load()
	if err != nil || !bytes.Equal(pin, genesis) {
		t.Fatalf("client pin = %x (%v), want %x", pin, err, genesis)
	}
}

func TestApplyPinRefusesWhenAnUnreachableNodeHoldsADifferentPin(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{Socket: filepath.Join(t.TempDir(), "absent.sock")}
	if err := nodePinFile().Save(testGenesis(0x44)); err != nil {
		t.Fatalf("seed node pin: %v", err)
	}

	err := applyPin(context.Background(), cfg, testGenesis(0x55), io.Discard, io.Discard)

	if err == nil {
		t.Fatal("a stopped node pinned to another genesis must still block the client pin")
	}
	pin, perr := clientPinFile().Load()
	if perr != nil || pin != nil {
		t.Fatalf("no client pin must be written: got %x, %v", pin, perr)
	}
}

func TestApplyPinPinsTheClientWhenNoNodeIsRunning(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{Socket: filepath.Join(t.TempDir(), "absent.sock")}
	genesis := testGenesis(0x66)

	if err := applyPin(context.Background(), cfg, genesis, io.Discard, io.Discard); err != nil {
		t.Fatalf("a client-only machine must still be pinnable: %v", err)
	}

	pin, err := clientPinFile().Load()
	if err != nil || !bytes.Equal(pin, genesis) {
		t.Fatalf("client pin = %x (%v), want %x", pin, err, genesis)
	}
}

func networkGenesisNode(t *testing.T) (*config.Config, []byte) {
	t.Helper()
	chain := makeTestChainBytes(t)
	raw, err := trustlog.ChainEntries(chain)
	if err != nil {
		t.Fatalf("ChainEntries: %v", err)
	}
	sync := func(_ context.Context, _ json.RawMessage) (any, error) {
		return api.TrustLogSyncResult{Entries: raw}, nil
	}
	cfg := &config.Config{Socket: serveFakeNode(t, map[string]api.HandlerFunc{
		api.MethodTrustLogSync: sync,
		api.MethodLockPin:      acceptPin,
	})}
	entries, err := trustlog.UnmarshalChain(chain)
	if err != nil {
		t.Fatalf("UnmarshalChain: %v", err)
	}
	return cfg, trustlog.HashEntry(&entries[0])
}

// TestPinFromNetworkRefusesAGenesisThatContradictsTheConfig covers the bare form the
// guide documents as primary: writing the network's genesis over a config pin bricks
// the next `argus` run with a genesis pin conflict.
func TestPinFromNetworkRefusesAGenesisThatContradictsTheConfig(t *testing.T) {
	tempStateDir(t)
	cfg, _ := networkGenesisNode(t)
	cfg.Lock.Genesis = trustpin.Encode(testGenesis(0x77))

	err := pinFromNetwork(context.Background(), cfg, strings.NewReader("y\n"), io.Discard)

	if err == nil {
		t.Fatal("the bare `lock pin` must refuse a genesis that contradicts lock.genesis")
	}
	if !strings.Contains(err.Error(), "lock.genesis") {
		t.Fatalf("error must name the config key, got: %v", err)
	}
	pin, perr := clientPinFile().Load()
	if perr != nil || pin != nil {
		t.Fatalf("no client pin must be written: got %x, %v", pin, perr)
	}
}

func TestPinFromNetworkPinsWhenTheConfigAgrees(t *testing.T) {
	tempStateDir(t)
	cfg, genesis := networkGenesisNode(t)
	cfg.Lock.Genesis = trustpin.Encode(genesis)

	if err := pinFromNetwork(context.Background(), cfg, strings.NewReader("y\n"), io.Discard); err != nil {
		t.Fatalf("pinning the genesis the config already names must be allowed: %v", err)
	}

	pin, err := clientPinFile().Load()
	if err != nil || !bytes.Equal(pin, genesis) {
		t.Fatalf("client pin = %x (%v), want %x", pin, err, genesis)
	}
}

// TestClientPinLineReturnsWhenTheGatewayNeverAnswers pins the diagnostic's contract: a
// gateway that completes the upgrade and answers nothing is exactly the degraded state
// `lock status` is run in, so the probe must be bounded and reported, not hang.
func TestClientPinLineReturnsWhenTheGatewayNeverAnswers(t *testing.T) {
	tempStateDir(t)
	blackhole := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := api.AcceptWS(w, r)
		if err != nil {
			return
		}
		defer conn.Close()
		<-blackhole
	}))
	t.Cleanup(func() {
		close(blackhole)
		ts.Close()
	})

	restore := gatewayProbeTimeout
	gatewayProbeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { gatewayProbeTimeout = restore })

	cfg := &config.Config{}
	cfg.Gateway.URL = "ws://" + strings.TrimPrefix(ts.URL, "http://")

	lines := make(chan string, 1)
	go func() { lines <- checkClientPin(context.Background(), cfg).text() }()

	select {
	case line := <-lines:
		if !strings.Contains(line, "did not answer") {
			t.Fatalf("line must report the unanswered probe, got: %q", line)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("lock status hung on a gateway that never answers")
	}
}

func TestGuardPinRefusesADifferentConfigGenesis(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{}
	cfg.Lock.Genesis = trustpin.Encode(testGenesis(0x77))

	err := guardPin(cfg, testGenesis(0x88))

	if err == nil {
		t.Fatal("writing a pin file that contradicts lock.genesis bricks the next start")
	}
	if !strings.Contains(err.Error(), "lock.genesis") {
		t.Fatalf("error must name the config key, got: %v", err)
	}
}

func TestGuardPinAllowsTheConfigGenesis(t *testing.T) {
	tempStateDir(t)
	genesis := testGenesis(0x99)
	cfg := &config.Config{}
	cfg.Lock.Genesis = trustpin.Encode(genesis)

	if err := guardPin(cfg, genesis); err != nil {
		t.Fatalf("pinning the genesis the config already names must be allowed: %v", err)
	}
}

// TestClientPinStatusNamesQuarantine backs the doc promise that `argus lock status`
// shows a quarantined device and the genesis it saw — the client-side quarantine has
// no other surface.
func TestClientPinStatusNamesQuarantine(t *testing.T) {
	seen := testGenesis(0x11)

	line := clientPinInfo{pin: trustpin.Pin{}, netGenesis: seen}.text()

	if !strings.Contains(line, "locked out") {
		t.Fatalf("line must name the lockout, got: %q", line)
	}
	if !strings.Contains(line, fingerprintOf(seen)) {
		t.Fatalf("line must show the genesis that was seen, got: %q", line)
	}
	if !strings.Contains(line, "argus lock pin") {
		t.Fatalf("line must name the fix, got: %q", line)
	}
}

// TestClientPinStatusNamesACorruptPin covers the pin file that exists but is the
// wrong length: the same file makes `argus` refuse to start, so reporting "none" is
// actively misleading.
func TestClientPinStatusNamesACorruptPin(t *testing.T) {
	dir := tempStateDir(t)
	if err := os.WriteFile(filepath.Join(dir, "client-trustlog-genesis"), []byte("short"), 0o600); err != nil {
		t.Fatalf("write corrupt pin: %v", err)
	}

	pin, perr := trustpin.Resolve("", clientPinFile())
	line := clientPinInfo{pin: pin, perr: perr}.text()

	if strings.Contains(line, "none") {
		t.Fatalf("a corrupt pin must not be reported as no pin, got: %q", line)
	}
	if !strings.Contains(line, "corrupt") {
		t.Fatalf("line must name the corruption, got: %q", line)
	}
}

func TestClientPinStatusReportsAPinnedClient(t *testing.T) {
	genesis := testGenesis(0x21)

	line := clientPinInfo{pin: trustpin.Pin{Genesis: genesis, Source: trustpin.SourceConfig}}.text()

	if !strings.Contains(line, fingerprintOf(genesis)) || !strings.Contains(line, "config") {
		t.Fatalf("line must show the fingerprint and its source, got: %q", line)
	}
}

func TestClientPinStatusUnpinnedOnAnUnlockedNetwork(t *testing.T) {
	line := clientPinInfo{pin: trustpin.Pin{}}.text()

	if strings.Contains(line, "locked out") {
		t.Fatalf("no trust log on the network must not claim a lockout, got: %q", line)
	}
}

// TestQuarantiningGenesisReportsCompetingRoots pins the difference between choosing a
// pin and reporting one: two roots is a hard error when picking, but the device is
// quarantined all the same, so status must still name a genesis.
func TestQuarantiningGenesisReportsCompetingRoots(t *testing.T) {
	chains := [][]byte{makeTestChainBytes(t), makeTestChainBytes(t)}
	var allEntries [][]byte
	for _, c := range chains {
		raw, err := trustlog.ChainEntries(c)
		if err != nil {
			t.Fatalf("ChainEntries: %v", err)
		}
		allEntries = append(allEntries, raw...)
	}
	sync := func(_ context.Context, _ json.RawMessage) (any, error) {
		return api.TrustLogSyncResult{Entries: allEntries}, nil
	}
	cfg := &config.Config{Socket: serveFakeNode(t, map[string]api.HandlerFunc{api.MethodTrustLogSync: sync})}

	got, err := quarantiningGenesis(context.Background(), cfg)

	if err != nil {
		t.Fatalf("quarantiningGenesis: %v", err)
	}
	entries, _ := trustlog.UnmarshalChain(chains[0])
	if want := trustlog.HashEntry(&entries[0]); !bytes.Equal(got, want) {
		t.Fatalf("genesis = %x, want the first decodable chain's %x", got, want)
	}
	if _, rerr := resolveGenesis(chains); rerr == nil {
		t.Fatal("precondition: resolveGenesis must reject competing roots")
	}
}

func TestQuarantiningGenesisReportsNoneOnAnUnlockedNetwork(t *testing.T) {
	sync := func(_ context.Context, _ json.RawMessage) (any, error) {
		return api.TrustLogSyncResult{}, nil
	}
	cfg := &config.Config{Socket: serveFakeNode(t, map[string]api.HandlerFunc{api.MethodTrustLogSync: sync})}

	got, err := quarantiningGenesis(context.Background(), cfg)

	if err != nil || got != nil {
		t.Fatalf("no chains → want (nil, nil), got (%x, %v)", got, err)
	}
}

// TestUnpinResolvesAConfigFileConflict is the escape hatch trustpin.Resolve's conflict
// error names: a device with lock.genesis X and a pin file holding Y refuses to start,
// and `lock unpin` is the only command that can clear the file.
func TestUnpinResolvesAConfigFileConflict(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{Socket: filepath.Join(t.TempDir(), "absent.sock")}
	cfg.Lock.Genesis = trustpin.Encode(testGenesis(0x12))
	if err := clientPinFile().Save(testGenesis(0x13)); err != nil {
		t.Fatalf("seed client pin: %v", err)
	}
	if _, rerr := trustpin.Resolve(cfg.Lock.Genesis, clientPinFile()); rerr == nil {
		t.Fatal("precondition: config X + file Y must be a conflict")
	}

	if err := unpinDevice(context.Background(), cfg, io.Discard, io.Discard); err != nil {
		t.Fatalf("unpin must be able to clear the file it is documented to clear: %v", err)
	}

	pin, perr := trustpin.Resolve(cfg.Lock.Genesis, clientPinFile())
	if perr != nil {
		t.Fatalf("the conflict must be gone after unpin: %v", perr)
	}
	if pin.Source != trustpin.SourceConfig {
		t.Fatalf("the device must stay pinned by config, got source %s", pin.Source)
	}
}

func TestUnpinClearsAFilePin(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{Socket: filepath.Join(t.TempDir(), "absent.sock")}
	if err := clientPinFile().Save(testGenesis(0x13)); err != nil {
		t.Fatalf("seed client pin: %v", err)
	}

	if err := unpinDevice(context.Background(), cfg, io.Discard, io.Discard); err != nil {
		t.Fatalf("a file-pinned device must be unpinnable: %v", err)
	}

	pin, perr := clientPinFile().Load()
	if perr != nil || pin != nil {
		t.Fatalf("client pin = %x (%v), want cleared", pin, perr)
	}
}

// TestUnpinClearsAStoppedNodesPinWhenTheConfigStillPinsIt closes the loop on the
// conflict: a node whose pin file disagrees with lock.genesis refuses to start, so it
// can never answer the unpin RPC and only the CLI can clear its file.
func TestUnpinClearsAStoppedNodesPinWhenTheConfigStillPinsIt(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{Socket: filepath.Join(t.TempDir(), "absent.sock")}
	cfg.Lock.Genesis = trustpin.Encode(testGenesis(0x14))
	if err := nodePinFile().Save(testGenesis(0x15)); err != nil {
		t.Fatalf("seed node pin: %v", err)
	}

	if err := unpinDevice(context.Background(), cfg, io.Discard, io.Discard); err != nil {
		t.Fatalf("unpinDevice: %v", err)
	}

	if _, rerr := trustpin.Resolve(cfg.Lock.Genesis, nodePinFile()); rerr != nil {
		t.Fatalf("the node must be startable again after unpin: %v", rerr)
	}
}

// TestUnpinLeavesAStoppedNodesPinAloneWithoutAConfigPin is the fail-closed direction:
// with nothing left to pin the node, clearing its file behind its back would widen
// what it accepts on its next start.
func TestUnpinLeavesAStoppedNodesPinAloneWithoutAConfigPin(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{Socket: filepath.Join(t.TempDir(), "absent.sock")}
	seeded := testGenesis(0x16)
	if err := nodePinFile().Save(seeded); err != nil {
		t.Fatalf("seed node pin: %v", err)
	}

	if err := unpinDevice(context.Background(), cfg, io.Discard, io.Discard); err != nil {
		t.Fatalf("unpinDevice: %v", err)
	}

	pin, perr := nodePinFile().Load()
	if perr != nil || !bytes.Equal(pin, seeded) {
		t.Fatalf("node pin = %x (%v), want the untouched %x", pin, perr, seeded)
	}
}

func TestUnpinSummaryNamesTheRemainingConfigPin(t *testing.T) {
	genesis := testGenesis(0x17)

	msg := unpinSummary(genesis)

	if strings.Contains(msg, "no trust root") {
		t.Fatalf("a config-pinned device is still pinned, got: %q", msg)
	}
	if !strings.Contains(msg, "lock.genesis") || !strings.Contains(msg, fingerprintOf(genesis)) {
		t.Fatalf("message must name lock.genesis and what it pins to, got: %q", msg)
	}
}

func TestUnpinSummarySaysNoTrustRootWithoutAConfigPin(t *testing.T) {
	msg := unpinSummary(nil)

	if !strings.Contains(msg, "no trust root") {
		t.Fatalf("a fully unpinned device has no trust root, got: %q", msg)
	}
}

func liveChainForTest(t *testing.T) (chain []byte, genesis []byte) {
	t.Helper()
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	log, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, nil)
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	return trustlog.MarshalChain(log.Entries()), log.Tip()
}

// disabledChainForTest returns the chain a device holds after `argus lock disable`:
// a genesis carrying one disablement commitment, plus the entry that consumed it.
func disabledChainForTest(t *testing.T) (chain []byte, genesis []byte) {
	t.Helper()
	signer, err := trustlog.GenerateSigner()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	secret, err := trustlog.GenerateDisablementSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	log, err := trustlog.NewGenesis([][]byte{signer.Public}, signer, [][]byte{trustlog.DisablementCommitment(secret)})
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	tip := log.Tip()
	if err := log.Disable(secret, signer); err != nil {
		t.Fatalf("disable: %v", err)
	}
	return trustlog.MarshalChain(log.Entries()), tip
}

// A client pin to a disabled chain is stale: `lock pin` adopts the live root in one
// command, exactly as it does on a device that never had a pin.
func TestGuardPinAllowsReplacingASupersededClientPin(t *testing.T) {
	tempStateDir(t)
	chain, genesis := disabledChainForTest(t)
	if err := clientPinFile().Save(genesis); err != nil {
		t.Fatalf("seed pin: %v", err)
	}
	if err := os.WriteFile(config.GetStatePath("client-trustlog-chain"), chain, 0o600); err != nil {
		t.Fatalf("seed chain: %v", err)
	}

	if err := guardPin(&config.Config{}, testGenesis(0xEF)); err != nil {
		t.Fatalf("a superseded pin must not block a new one: %v", err)
	}
}

// A live pin still requires an explicit unpin.
func TestGuardPinStillRefusesALiveClientPin(t *testing.T) {
	tempStateDir(t)
	chain, genesis := liveChainForTest(t)
	if err := clientPinFile().Save(genesis); err != nil {
		t.Fatalf("seed pin: %v", err)
	}
	if err := os.WriteFile(config.GetStatePath("client-trustlog-chain"), chain, 0o600); err != nil {
		t.Fatalf("seed chain: %v", err)
	}

	if err := guardPin(&config.Config{}, testGenesis(0xEF)); err == nil {
		t.Fatal("a live pin must still require unpin")
	}
}

// Without the old chain there is no evidence the pin is stale; stay conservative.
func TestGuardPinRefusesWhenStalenessCannotBeProven(t *testing.T) {
	tempStateDir(t)
	if err := clientPinFile().Save(testGenesis(0xCD)); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	if err := guardPin(&config.Config{}, testGenesis(0xEF)); err == nil {
		t.Fatal("no chain on disk means no proof of staleness")
	}
}

// The client role is pinned separately, so after a relock it must say so on its own
// line — otherwise the only signal is the node's, and a dark dashboard looks fine.
func TestClientPinStatusNamesSupersession(t *testing.T) {
	line := clientPinInfo{pin: trustpin.Pin{Genesis: testGenesis(0xC1), Source: trustpin.SourceFile}, netGenesis: testGenesis(0xC2), superseded: true}.text()

	for _, want := range []string{"new root", "argus lock pin", "restart"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q must contain %q", line, want)
		}
	}
}

// Supersession is proven by the disabled chain, not by the network probe; report it
// even when the gateway could not be reached for the new fingerprint.
func TestClientPinStatusNamesSupersessionWithoutTheNetwork(t *testing.T) {
	line := clientPinInfo{pin: trustpin.Pin{Genesis: testGenesis(0xC1), Source: trustpin.SourceFile}, superseded: true}.text()

	if !strings.Contains(line, "no new root") {
		t.Fatalf("line %q must still report supersession", line)
	}
}

// A superseded node cannot be authorized into a chain it does not follow; the only
// useful instruction is to pin.
func TestAuthorizeHintSuppressedWhenThereIsNoLiveRoot(t *testing.T) {
	id := testGenesis(0x44)
	if h := authorizeHint(api.LockStatusResult{Enabled: true, IdentityPubKey: id}); h == "" {
		t.Fatal("an enforcing chain that lacks this node must still hint how to authorize it")
	}
	if h := authorizeHint(api.LockStatusResult{Enabled: true, Disabled: true, IdentityPubKey: id}); h != "" {
		t.Fatalf("a disabled chain authorizes nobody; got %q", h)
	}
	if h := authorizeHint(api.LockStatusResult{Enabled: true, Quarantined: true, IdentityPubKey: id}); h != "" {
		t.Fatalf("a quarantined device must be told to pin, not to sign; got %q", h)
	}
	if h := authorizeHint(api.LockStatusResult{Enabled: true, Authorized: true, IdentityPubKey: id}); h != "" {
		t.Fatalf("an authorized node needs no hint; got %q", h)
	}
}

// On a node-only machine the client role may never have run, so the only proof this
// device's root is dead is the NODE's persisted chain. Reading just the client's left
// `lock pin` refusing on the very device the parity work exists to unstick.
func TestGuardPinAllowsReplacementProvenByTheNodeChain(t *testing.T) {
	tempStateDir(t)
	chain, genesis := disabledChainForTest(t)
	if err := clientPinFile().Save(genesis); err != nil {
		t.Fatalf("seed pin: %v", err)
	}
	if err := os.WriteFile(config.GetStatePath("trustlog-chain"), chain, 0o600); err != nil {
		t.Fatalf("seed node chain: %v", err)
	}

	if err := guardPin(&config.Config{}, testGenesis(0xEF)); err != nil {
		t.Fatalf("a pin the node's own chain proves dead must not block: %v", err)
	}
}

// A live node chain still guards the pin: only a disabled one makes it stale.
func TestGuardPinRefusesWhenTheNodeChainIsLive(t *testing.T) {
	tempStateDir(t)
	chain, genesis := liveChainForTest(t)
	if err := clientPinFile().Save(genesis); err != nil {
		t.Fatalf("seed pin: %v", err)
	}
	if err := os.WriteFile(config.GetStatePath("trustlog-chain"), chain, 0o600); err != nil {
		t.Fatalf("seed node chain: %v", err)
	}

	if err := guardPin(&config.Config{}, testGenesis(0xEF)); err == nil {
		t.Fatal("a live node chain must still require unpin")
	}
}

func TestApplyPinWritesItsReportToTheGivenWriters(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{Socket: serveFakeNode(t, map[string]api.HandlerFunc{api.MethodLockPin: acceptPin})}
	var out, notes strings.Builder
	var err error

	stdout, stderr := captureOutput(t, func() {
		err = applyPin(context.Background(), cfg, testGenesis(0x71), &out, &notes)
	})

	if err != nil {
		t.Fatalf("applyPin: %v", err)
	}
	for _, want := range []string{"pinned this device", "local node pinned and enforcing"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out must contain %q, got: %q", want, out.String())
		}
	}
	if notes.Len() != 0 {
		t.Errorf("notes = %q, want empty", notes.String())
	}
	if stdout != "" || stderr != "" {
		t.Errorf("applyPin printed stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestApplyPinWritesTheUnreachableNodeNote(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{Socket: filepath.Join(t.TempDir(), "absent.sock")}
	var notes strings.Builder
	var err error

	_, stderr := captureOutput(t, func() {
		err = applyPin(context.Background(), cfg, testGenesis(0x72), io.Discard, &notes)
	})

	if err != nil {
		t.Fatalf("applyPin: %v", err)
	}
	if !strings.Contains(notes.String(), "the local node is unreachable") {
		t.Errorf("notes must carry the unreachable-node note, got: %q", notes.String())
	}
	if stderr != "" {
		t.Errorf("applyPin printed to stderr: %q", stderr)
	}
}

func TestUnpinDeviceWritesItsReportToTheGivenWriters(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{Socket: filepath.Join(t.TempDir(), "absent.sock")}
	var out, notes strings.Builder
	var err error

	stdout, stderr := captureOutput(t, func() {
		err = unpinDevice(context.Background(), cfg, &out, &notes)
	})

	if err != nil {
		t.Fatalf("unpinDevice: %v", err)
	}
	if !strings.Contains(out.String(), "cleared this device's persisted pin") {
		t.Errorf("out = %q", out.String())
	}
	if !strings.Contains(notes.String(), unpinSummary(nil)) {
		t.Errorf("notes must contain the unpin summary, got: %q", notes.String())
	}
	if stdout != "" || stderr != "" {
		t.Errorf("unpinDevice printed stdout=%q stderr=%q", stdout, stderr)
	}
}

func blockRemoval(t *testing.T, dir, name string) {
	t.Helper()
	blocked := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Join(blocked, "keep"), 0o700); err != nil {
		t.Fatalf("block %s: %v", name, err)
	}
}

func TestApplyPinKeepsTheNodeNoteWhenALaterStepFails(t *testing.T) {
	dir := tempStateDir(t)
	cfg := &config.Config{Socket: filepath.Join(t.TempDir(), "absent.sock")}
	blockRemoval(t, dir, "client-trustlog-chain")
	var out, notes strings.Builder

	err := applyPin(context.Background(), cfg, testGenesis(0x73), &out, &notes)

	if err == nil {
		t.Fatal("a chain that cannot be removed must fail the pin")
	}
	if out.Len() != 0 || !strings.Contains(notes.String(), "the local node is unreachable") {
		t.Fatalf("out = %q, notes = %q; the unreachable-node note must survive the error", out.String(), notes.String())
	}
}

type streamPart struct {
	stderr bool
	text   string
}

type streamWriter struct {
	stderr bool
	parts  *[]streamPart
}

func (w streamWriter) Write(p []byte) (int, error) {
	*w.parts = append(*w.parts, streamPart{stderr: w.stderr, text: string(p)})
	return len(p), nil
}

func TestUnpinDeviceKeepsTheOriginalStreamOrder(t *testing.T) {
	dir := tempStateDir(t)
	cfg := &config.Config{Socket: filepath.Join(t.TempDir(), "absent.sock")}
	cfg.Lock.Genesis = trustpin.Encode(testGenesis(0x74))
	blockRemoval(t, dir, "trustlog-chain")
	var parts []streamPart

	err := unpinDevice(context.Background(), cfg, streamWriter{parts: &parts}, streamWriter{stderr: true, parts: &parts})

	if err != nil {
		t.Fatalf("unpinDevice: %v", err)
	}
	wantStderr := []bool{false, true, false, true}
	wantText := []string{"cleared this device's persisted pin", "stored chain could not be removed", "local node is not running", "lock.genesis"}
	if len(parts) != len(wantText) {
		t.Fatalf("got %d parts, want %d: %+v", len(parts), len(wantText), parts)
	}
	for i, p := range parts {
		if p.stderr != wantStderr[i] || !strings.Contains(p.text, wantText[i]) {
			t.Errorf("part %d = %+v, want stderr=%v containing %q", i, p, wantStderr[i], wantText[i])
		}
	}
}

func TestApplyPinAbortsWhenTheNodeDoesNotAnswerInTime(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{Socket: serveFakeNode(t, map[string]api.HandlerFunc{api.MethodLockPin: blockingHandler(t)})}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := applyPin(ctx, cfg, testGenesis(0x75), io.Discard, io.Discard)

	if err == nil {
		t.Fatal("a node that does not answer in time must abort the pin")
	}
	if !strings.Contains(err.Error(), "may still apply the pin") || !strings.Contains(err.Error(), "client pin was not written") {
		t.Fatalf("error = %q, want the may-still-apply and not-written wording", err)
	}
	if pin, perr := clientPinFile().Load(); perr != nil || pin != nil {
		t.Fatalf("client pin = %x (%v), want none written", pin, perr)
	}
}

func TestUnpinDeviceKeepsTheNodePinWhenTheNodeDoesNotAnswerInTime(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{Socket: serveFakeNode(t, map[string]api.HandlerFunc{api.MethodLockUnpin: blockingHandler(t)})}
	g := testGenesis(0x76)
	cfg.Lock.Genesis = trustpin.Encode(g)
	if err := nodePinFile().Save(g); err != nil {
		t.Fatalf("seed node pin: %v", err)
	}
	if err := os.WriteFile(config.GetStatePath("trustlog-chain"), []byte("chain"), 0o600); err != nil {
		t.Fatalf("seed node chain: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := unpinDevice(ctx, cfg, io.Discard, io.Discard)

	if err == nil {
		t.Fatal("a node that does not answer in time must fail the unpin")
	}
	for _, want := range []string{"did not answer in time", "may still remove its pin", "this device's persisted pin was cleared"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
	if pin, perr := nodePinFile().Load(); perr != nil || !bytes.Equal(pin, g) {
		t.Fatalf("node pin = %x (%v), want the untouched %x", pin, perr, g)
	}
	if _, serr := os.Stat(config.GetStatePath("trustlog-chain")); serr != nil {
		t.Fatalf("node chain: %v, want it kept", serr)
	}
}
