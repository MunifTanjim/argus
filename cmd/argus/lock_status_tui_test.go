package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/e2e"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("reading captured stdout: %v", err)
	}
	r.Close()
	return buf.String()
}

func TestKeyAuthorized(t *testing.T) {
	a, b := testGenesis(0x01), testGenesis(0x02)
	if !keyAuthorized(a, [][]byte{b, a}) {
		t.Error("a member key must be authorized")
	}
	if keyAuthorized(a, [][]byte{b}) {
		t.Error("a non-member key must not be authorized")
	}
	if keyAuthorized(a, nil) {
		t.Error("an empty set authorizes nobody")
	}
}

func TestDevicesKnown(t *testing.T) {
	if !devicesKnown(api.LockStatusResult{}) {
		t.Error("no devices is a known (empty) set")
	}
	if !devicesKnown(api.LockStatusResult{DeviceCount: 2, Devices: [][]byte{testGenesis(0x1), testGenesis(0x2)}}) {
		t.Error("a populated list is known")
	}
	if devicesKnown(api.LockStatusResult{DeviceCount: 2}) {
		t.Error("a count with no list (stale daemon) must be treated as unknown")
	}
}

// The gap this closes: while the node is up, the tui's own key and its sign command
// must be reachable, so an operator can authorize the dashboard without stopping the node.
func TestPrintTUIRoleUnauthorizedShowsSignHint(t *testing.T) {
	tempStateDir(t)
	out := captureStdout(t, func() {
		_ = printTUIRole(context.Background(), &config.Config{}, api.LockStatusResult{
			Enabled: true, DeviceCount: 1, Devices: [][]byte{testGenesis(0x99)},
		})
	})
	for _, want := range []string{"this tui", "authorized: no", "argus lock sign"} {
		if !strings.Contains(out, want) {
			t.Errorf("output must contain %q, got:\n%s", want, out)
		}
	}
}

func TestPrintTUIRoleAuthorizedHidesSignHint(t *testing.T) {
	tempStateDir(t)
	kp, err := e2e.LoadOrCreateIdentity(config.GetStatePath("client-identity.json"))
	if err != nil {
		t.Fatalf("client identity: %v", err)
	}
	out := captureStdout(t, func() {
		_ = printTUIRole(context.Background(), &config.Config{}, api.LockStatusResult{
			Enabled: true, DeviceCount: 1, Devices: [][]byte{kp.Public},
		})
	})
	if !strings.Contains(out, "authorized: yes") {
		t.Errorf("output must show 'authorized: yes', got:\n%s", out)
	}
	if strings.Contains(out, "argus lock sign") {
		t.Errorf("an authorized tui must not show a sign hint, got:\n%s", out)
	}
}
