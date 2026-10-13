package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

// ListenUnix clears a stale socket at path, then listens owner-only there.
func ListenUnix(path string, log *slog.Logger) (net.Listener, error) {
	if err := ClearStaleSocket(path); err != nil {
		return nil, err
	}
	return ListenOwnerOnly(path, log)
}

// ListenOwnerOnly listens on a unix socket at path that only its owner can use.
// The umask keeps the socket private from bind to chmod. It is process-wide, so
// a file made meanwhile is owner-only too. Some filesystems (bind mounts under
// colima) reject chmod on a socket, which then keeps the umask mode.
func ListenOwnerOnly(path string, log *slog.Logger) (net.Listener, error) {
	old := syscall.Umask(0o077)
	l, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		log.Warn("restricting socket mode failed", "path", path, "err", err)
	}
	return l, nil
}

// ClearStaleSocket removes a leftover socket at path that no process answers.
// A live one is an error, so a second process never unlinks the socket of a
// running one.
func ClearStaleSocket(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	conn, err := net.Dial("unix", path)
	if err == nil {
		conn.Close()
		return fmt.Errorf("an argus process is already running at %s", path)
	}
	if !SocketAbsent(err) {
		return fmt.Errorf("probing socket %s: %w", path, err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing stale socket %s: %w", path, err)
	}
	return nil
}

// SocketAbsent reports whether a dial error means that no process listens:
// a missing socket (ENOENT) or a stale one with no listener (ECONNREFUSED).
func SocketAbsent(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}
