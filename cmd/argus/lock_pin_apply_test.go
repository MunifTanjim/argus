package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	pull := func(_ context.Context, _ json.RawMessage) (any, error) {
		return api.TrustLogPullResult{Chains: [][]byte{chain}}, nil
	}
	cfg := &config.Config{Socket: serveFakeNode(t, map[string]api.HandlerFunc{
		api.MethodTrustLogPull: pull,
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
	cfg.Lock.Genesis = base64.StdEncoding.EncodeToString(testGenesis(0x77))

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
	cfg.Lock.Genesis = base64.StdEncoding.EncodeToString(genesis)

	if err := pinFromNetwork(context.Background(), cfg, strings.NewReader("y\n"), io.Discard); err != nil {
		t.Fatalf("pinning the genesis the config already names must be allowed: %v", err)
	}

	pin, err := clientPinFile().Load()
	if err != nil || !bytes.Equal(pin, genesis) {
		t.Fatalf("client pin = %x (%v), want %x", pin, err, genesis)
	}
}

func TestGuardPinRefusesADifferentConfigGenesis(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{}
	cfg.Lock.Genesis = base64.StdEncoding.EncodeToString(testGenesis(0x77))

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
	cfg.Lock.Genesis = base64.StdEncoding.EncodeToString(genesis)

	if err := guardPin(cfg, genesis); err != nil {
		t.Fatalf("pinning the genesis the config already names must be allowed: %v", err)
	}
}

// TestPinClientRolePinsTheMachineThatRanLockInit covers the dark-dashboard case: the
// client is a separate role with its own file, so lock.init must pin it too.
func TestPinClientRolePinsTheMachineThatRanLockInit(t *testing.T) {
	tempStateDir(t)
	genesis := testGenesis(0xAB)

	pinClientRole(&config.Config{}, genesis)

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

	pinClientRole(&config.Config{}, testGenesis(0xEF))

	pin, err := clientPinFile().Load()
	if err != nil || !bytes.Equal(pin, existing) {
		t.Fatalf("client pin = %x (%v), want the untouched %x", pin, err, existing)
	}
}

// TestClientPinStatusNamesQuarantine backs the doc promise that `argus lock status`
// shows a quarantined device and the genesis it saw — the client-side quarantine has
// no other surface.
func TestClientPinStatusNamesQuarantine(t *testing.T) {
	seen := testGenesis(0x11)

	line := clientPinStatus(trustpin.Pin{}, nil, seen, nil)

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
	line := clientPinStatus(pin, perr, nil, nil)

	if strings.Contains(line, "none") {
		t.Fatalf("a corrupt pin must not be reported as no pin, got: %q", line)
	}
	if !strings.Contains(line, "corrupt") {
		t.Fatalf("line must name the corruption, got: %q", line)
	}
}

func TestClientPinStatusReportsAPinnedClient(t *testing.T) {
	genesis := testGenesis(0x21)

	line := clientPinStatus(trustpin.Pin{Genesis: genesis, Source: trustpin.SourceConfig}, nil, nil, nil)

	if !strings.Contains(line, fingerprintOf(genesis)) || !strings.Contains(line, "config") {
		t.Fatalf("line must show the fingerprint and its source, got: %q", line)
	}
}

func TestClientPinStatusUnpinnedOnAnUnlockedNetwork(t *testing.T) {
	line := clientPinStatus(trustpin.Pin{}, nil, nil, nil)

	if strings.Contains(line, "QUARANTINED") {
		t.Fatalf("no trust log on the network must not claim quarantine, got: %q", line)
	}
}

// TestQuarantiningGenesisReportsCompetingRoots pins the difference between choosing a
// pin and reporting one: two roots is a hard error when picking, but the device is
// quarantined all the same, so status must still name a genesis.
func TestQuarantiningGenesisReportsCompetingRoots(t *testing.T) {
	chains := [][]byte{makeTestChainBytes(t), makeTestChainBytes(t)}
	pull := func(_ context.Context, _ json.RawMessage) (any, error) {
		return api.TrustLogPullResult{Chains: chains}, nil
	}
	cfg := &config.Config{Socket: serveFakeNode(t, map[string]api.HandlerFunc{api.MethodTrustLogPull: pull})}

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
	pull := func(_ context.Context, _ json.RawMessage) (any, error) {
		return api.TrustLogPullResult{}, nil
	}
	cfg := &config.Config{Socket: serveFakeNode(t, map[string]api.HandlerFunc{api.MethodTrustLogPull: pull})}

	got, err := quarantiningGenesis(context.Background(), cfg)

	if err != nil || got != nil {
		t.Fatalf("no chains → want (nil, nil), got (%x, %v)", got, err)
	}
}

// TestGuardUnpinRefusesAConfigPinnedDevice covers the flatly-wrong message: clearing
// the file on a config-pinned device leaves it pinned and enforcing.
func TestGuardUnpinRefusesAConfigPinnedDevice(t *testing.T) {
	tempStateDir(t)
	cfg := &config.Config{}
	cfg.Lock.Genesis = base64.StdEncoding.EncodeToString(testGenesis(0x12))

	err := guardUnpin(cfg)

	if err == nil {
		t.Fatal("unpin must refuse when the effective pin comes from the config")
	}
	if !strings.Contains(err.Error(), "lock.genesis") {
		t.Fatalf("error must name the config key, got: %v", err)
	}
}

func TestGuardUnpinAllowsAFilePinnedDevice(t *testing.T) {
	tempStateDir(t)
	if err := clientPinFile().Save(testGenesis(0x13)); err != nil {
		t.Fatalf("seed client pin: %v", err)
	}

	if err := guardUnpin(&config.Config{}); err != nil {
		t.Fatalf("a file-pinned device must be unpinnable: %v", err)
	}
}
