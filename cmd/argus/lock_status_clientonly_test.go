package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLockStatusClientOnlyDetection covers the two sides of the ENOENT split in
// newLockStatusCmd: a default socket that simply does not exist (client-only
// machine) must exit 0 and still print the device identity; an explicit
// --socket pointing at the same absent path must exit non-zero because the
// operator named a socket they expected to find.
func TestLockStatusClientOnlyDetection(t *testing.T) {
	tempStateDir(t)
	// Use MkdirTemp with a short prefix: macOS caps Unix socket paths at 104 bytes,
	// and t.TempDir() produces paths that regularly exceed that limit.
	dir, err := os.MkdirTemp("", "cl")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	absent := filepath.Join(dir, "absent.sock")

	t.Run("default socket absent exits 0 and prints identity", func(t *testing.T) {
		// Steer the resolved socket path to the known-absent socket without
		// touching cmd.Flags(), so Changed("socket") stays false.
		t.Setenv("ARGUS_SOCKET", absent)

		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("os.Pipe: %v", err)
		}
		oldOut := os.Stdout
		os.Stdout = w
		cmd := newLockStatusCmd()
		runErr := cmd.Execute()
		w.Close()
		os.Stdout = oldOut

		var buf bytes.Buffer
		if _, err := io.Copy(&buf, r); err != nil {
			t.Fatalf("reading captured stdout: %v", err)
		}
		r.Close()

		if runErr != nil {
			t.Fatalf("client-only machine (default socket absent) must exit 0, got: %v", runErr)
		}
		if !strings.Contains(buf.String(), "this device identity:") {
			t.Errorf("stdout missing device identity line; got:\n%s", buf.String())
		}
	})

	t.Run("explicit --socket absent exits non-zero", func(t *testing.T) {
		cmd := newLockStatusCmd()
		cmd.SetArgs([]string{"--socket", absent})
		if err := cmd.Execute(); err == nil {
			t.Fatal("expected non-zero exit when --socket is explicit and absent")
		}
	})
}
