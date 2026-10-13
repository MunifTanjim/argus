package api

import (
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ar")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func TestListenUnixRemovesStaleSocket(t *testing.T) {
	p := shortSocket(t)
	dead, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	dead.(*net.UnixListener).SetUnlinkOnClose(false)
	dead.Close()
	l, err := ListenUnix(p, slog.Default())
	if err != nil {
		t.Fatalf("ListenUnix over a stale socket: %v", err)
	}
	l.Close()
}

func TestListenUnixRefusesLiveSocket(t *testing.T) {
	p := shortSocket(t)
	live, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if _, err := ListenUnix(p, slog.Default()); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("err = %v, want already running", err)
	}
}

func TestSocketAbsent(t *testing.T) {
	if !SocketAbsent(syscall.ENOENT) || !SocketAbsent(syscall.ECONNREFUSED) {
		t.Fatal("ENOENT/ECONNREFUSED should count as absent")
	}
	if SocketAbsent(errors.New("some other error")) {
		t.Fatal("unrelated errors should not count as absent")
	}
	if _, err := Dial(shortSocket(t)); err == nil || !SocketAbsent(err) {
		t.Fatalf("missing socket should be absent, got %v", err)
	}
}

func TestListenUnixIsOwnerOnly(t *testing.T) {
	p := shortSocket(t)
	l, err := ListenUnix(p, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("socket mode = %o, want 600", mode)
	}
}
