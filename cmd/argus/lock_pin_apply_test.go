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

// tempStateDir points config.GetStatePath (and therefore both pin files) at a
// throwaway directory for the duration of one test.
func tempStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := config.StateDir
	config.StateDir = dir
	t.Cleanup(func() { config.StateDir = old })
	return dir
}

// serveFakeNode starts a local socket serving the given handlers. The socket lives in
// a short temp dir: macOS caps unix socket paths at ~104 bytes.
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

	err := applyPin(context.Background(), cfg, testGenesis(0x22))

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

	if err := applyPin(context.Background(), cfg, genesis); err != nil {
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

	err := applyPin(context.Background(), cfg, testGenesis(0x55))

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

	if err := applyPin(context.Background(), cfg, genesis); err != nil {
		t.Fatalf("a client-only machine must still be pinnable: %v", err)
	}

	pin, err := clientPinFile().Load()
	if err != nil || !bytes.Equal(pin, genesis) {
		t.Fatalf("client pin = %x (%v), want %x", pin, err, genesis)
	}
}

// networkGenesisNode serves one decodable chain plus an accepting lock.pin, i.e. the
// happy path the bare `argus lock pin` walks.
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
	go func() { lines <- clientPinLine(context.Background(), cfg) }()

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

// TestPinClientRolePinsTheMachineThatRanLockInit covers the dark-dashboard case: the
// client is a separate role with its own file, so lock.init must pin it too.
func TestPinClientRolePinsTheMachineThatRanLockInit(t *testing.T) {
	tempStateDir(t)
	genesis := testGenesis(0xAB)

	pinClientRole(&config.Config{}, genesis, false)

	pin, err := clientPinFile().Load()
	if err != nil || !bytes.Equal(pin, genesis) {
		t.Fatalf("client pin = %x (%v), want %x", pin, err, genesis)
	}
}

func TestPinClientRoleLeavesAConflictingPinAlone(t *testing.T) {
	tempStateDir(t)
	existing := testGenesis(0xCD)
	if err := clientPinFile().Save(existing); err != nil {
		t.Fatalf("seed client pin: %v", err)
	}

	pinClientRole(&config.Config{}, testGenesis(0xEF), false)

	pin, err := clientPinFile().Load()
	if err != nil || !bytes.Equal(pin, existing) {
		t.Fatalf("client pin = %x (%v), want the untouched %x", pin, err, existing)
	}
}

// Re-initing over a disabled log leaves the client pinned to a dead genesis, which
// would dark-dashboard the very machine that just relocked the network.
func TestPinClientRoleReplacesTheStalePinOnReinit(t *testing.T) {
	tempStateDir(t)
	if err := clientPinFile().Save(testGenesis(0xCD)); err != nil {
		t.Fatalf("seed client pin: %v", err)
	}
	fresh := testGenesis(0xEF)

	pinClientRole(&config.Config{}, fresh, true)

	pin, err := clientPinFile().Load()
	if err != nil || !bytes.Equal(pin, fresh) {
		t.Fatalf("client pin = %x (%v), want %x", pin, err, fresh)
	}
}

// A config pin is the operator's own declaration, so re-init must not overwrite it
// behind their back — the next `argus` run would die on the conflict.
func TestPinClientRoleKeepsTheConfigPinOnReinit(t *testing.T) {
	tempStateDir(t)
	declared := testGenesis(0xCD)
	cfg := &config.Config{}
	cfg.Lock.Genesis = trustpin.Encode(declared)

	pinClientRole(cfg, testGenesis(0xEF), true)

	pin, err := clientPinFile().Load()
	if err != nil || pin != nil {
		t.Fatalf("client pin = %x (%v), want no pin file written", pin, err)
	}
}

// TestClientPinStatusNamesQuarantine backs the doc promise that `argus lock status`
// shows a quarantined device and the genesis it saw — the client-side quarantine has
// no other surface.
func TestClientPinStatusNamesQuarantine(t *testing.T) {
	seen := testGenesis(0x11)

	line := clientPinStatus(trustpin.Pin{}, nil, seen, nil, false)

	if !strings.Contains(line, "QUARANTINED") {
		t.Fatalf("line must name the quarantine, got: %q", line)
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
	line := clientPinStatus(pin, perr, nil, nil, false)

	if strings.Contains(line, "none") {
		t.Fatalf("a corrupt pin must not be reported as no pin, got: %q", line)
	}
	if !strings.Contains(line, "corrupt") {
		t.Fatalf("line must name the corruption, got: %q", line)
	}
}

func TestClientPinStatusReportsAPinnedClient(t *testing.T) {
	genesis := testGenesis(0x21)

	line := clientPinStatus(trustpin.Pin{Genesis: genesis, Source: trustpin.SourceConfig}, nil, nil, nil, false)

	if !strings.Contains(line, fingerprintOf(genesis)) || !strings.Contains(line, "config") {
		t.Fatalf("line must show the fingerprint and its source, got: %q", line)
	}
}

func TestClientPinStatusUnpinnedOnAnUnlockedNetwork(t *testing.T) {
	line := clientPinStatus(trustpin.Pin{}, nil, nil, nil, false)

	if strings.Contains(line, "QUARANTINED") {
		t.Fatalf("no trust log on the network must not claim quarantine, got: %q", line)
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

	if err := unpinDevice(context.Background(), cfg); err != nil {
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

	if err := unpinDevice(context.Background(), cfg); err != nil {
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

	if err := unpinDevice(context.Background(), cfg); err != nil {
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

	if err := unpinDevice(context.Background(), cfg); err != nil {
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

// liveChainForTest returns a genesis-only chain and its genesis hash.
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
	line := clientPinStatus(trustpin.Pin{Genesis: testGenesis(0xC1), Source: trustpin.SourceFile}, nil, testGenesis(0xC2), nil, true)

	for _, want := range []string{"SUPERSEDED", "argus lock pin", "restart"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q must contain %q", line, want)
		}
	}
}

// Supersession is proven by the disabled chain, not by the network probe; report it
// even when the gateway could not be reached for the new fingerprint.
func TestClientPinStatusNamesSupersessionWithoutTheNetwork(t *testing.T) {
	line := clientPinStatus(trustpin.Pin{Genesis: testGenesis(0xC1), Source: trustpin.SourceFile}, nil, nil, nil, true)

	if !strings.Contains(line, "SUPERSEDED") {
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
