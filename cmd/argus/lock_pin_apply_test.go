package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
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

// serveFakeNode starts a local node socket serving lock.pin with the given handler.
// The socket lives in a short temp dir: macOS caps unix socket paths at ~104 bytes.
func serveFakeNode(t *testing.T, lockPin api.HandlerFunc) string {
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
	srv.Handle(api.MethodLockPin, lockPin)
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
	cfg := &config.Config{Socket: serveFakeNode(t, refusePin)}

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
	cfg := &config.Config{Socket: serveFakeNode(t, acceptPin)}
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
