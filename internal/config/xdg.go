package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

var HomeDir = xdg.Home
var CacheDir = filepath.Join(xdg.CacheHome, ProjectName)
var ConfigDir = filepath.Join(xdg.ConfigHome, ProjectName)
var StateDir = filepath.Join(xdg.StateHome, ProjectName)
var RuntimeDir = resolveRuntimeDir()

// RuntimeDirIsFallback reports whether RuntimeDir fell back to a temp-dir path
// because the XDG runtime dir was unusable. `argus start` surfaces this so the
// user understands why the local socket lives outside /run/user/<uid>.
var RuntimeDirIsFallback bool

// resolveRuntimeDir returns argus's per-user runtime directory. It prefers the
// XDG runtime dir (/run/user/<uid> on Linux), but that directory is created by
// systemd-logind on login and is absent in sessions without one (su, cron, some
// SSH setups, containers). When it is unusable, fall back to a per-user dir under
// the system temp dir, as the XDG spec advises, and record that we did so.
func resolveRuntimeDir() string {
	if runtimeDirUsable(xdg.RuntimeDir) {
		return filepath.Join(xdg.RuntimeDir, ProjectName)
	}
	RuntimeDirIsFallback = true
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", ProjectName, os.Getuid()))
}

// runtimeDirUsable reports whether base exists and is a writable directory. It
// probes writability with a temp entry it immediately removes, leaving no trace,
// so this is safe to call at init from every argus command.
func runtimeDirUsable(base string) bool {
	if base == "" {
		return false
	}
	if fi, err := os.Stat(base); err != nil || !fi.IsDir() {
		return false
	}
	probe, err := os.MkdirTemp(base, ".argus-probe-")
	if err != nil {
		return false
	}
	_ = os.Remove(probe)
	return true
}

func GetCachePath(name string) string {
	return filepath.Join(CacheDir, name)
}

func GetConfigPath(name string) string {
	return filepath.Join(ConfigDir, name)
}

func GetStatePath(name string) string {
	return filepath.Join(StateDir, name)
}

func GetRuntimePath(name string) string {
	return filepath.Join(RuntimeDir, name)
}
