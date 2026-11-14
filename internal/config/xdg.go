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
var DataDir = filepath.Join(xdg.DataHome, ProjectName)
var StateDir = filepath.Join(xdg.StateHome, ProjectName)
var RuntimeDir = resolveRuntimeDir()

// RuntimeDirIsFallback reports whether RuntimeDir fell back to a temp-dir path
// because the XDG runtime dir was unusable. `argus start` surfaces this.
var RuntimeDirIsFallback bool

// resolveRuntimeDir prefers the XDG runtime dir (/run/user/<uid>), but it's absent
// in sessions without systemd-logind (su, cron, some SSH, containers). Falls back to
// a per-user temp dir, as the XDG spec advises.
func resolveRuntimeDir() string {
	dir, fallback := pickRuntimeDir(xdg.RuntimeDir, os.TempDir())
	RuntimeDirIsFallback = fallback
	return dir
}

// pickRuntimeDir reuses an existing <base>/argus without a write probe, since a
// sandboxed client can connect to a node's directory it cannot write to.
func pickRuntimeDir(base, tempDir string) (dir string, fallback bool) {
	if base != "" {
		dir := filepath.Join(base, ProjectName)
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir, false
		}
		if runtimeDirUsable(base) {
			return dir, false
		}
	}
	return filepath.Join(tempDir, fmt.Sprintf("%s-%d", ProjectName, os.Getuid())), true
}

// runtimeDirUsable reports whether base is a writable directory, probing with a temp
// entry it immediately removes (safe to call at init from every command).
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

func GetDataPath(name string) string {
	return filepath.Join(DataDir, name)
}

func GetStatePath(name string) string {
	return filepath.Join(StateDir, name)
}

func GetRuntimePath(name string) string {
	return filepath.Join(RuntimeDir, name)
}
