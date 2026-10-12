package claudecode

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/shell"
)

// minClaudeVersion is the first Claude Code release the mod supports.
const minClaudeVersion = "2.1.293"

const (
	marketplaceName = "argus"
	pluginName      = "argus"
	pluginID        = pluginName + "@" + marketplaceName
	claudeTimeout   = 2 * time.Minute
)

//go:embed all:mod
var modFS embed.FS

// Replaced by tests.
var (
	marketplaceDir = func() string { return config.GetDataPath("claude-marketplace") }
	claudeOnPath   = func() bool { return shell.ExecutableExists("claude") }
	runClaude      = func(ctx context.Context, stdin string, args ...string) ([]byte, error) {
		cmd := shell.NewCommandContext(ctx, "claude", args...).WithStdIn(strings.NewReader(stdin))
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("claude %s: %w: %s", strings.Join(args, " "), err, cmd.StdErr().TrimSpace())
		}
		return []byte(cmd.StdOut()), nil
	}
)

// installable reports whether an embedded file ships with the installed mod:
// tests and the type files Claude Code writes beside a loaded mod stay behind.
func installable(name string) bool {
	return !strings.HasSuffix(name, ".test.ts") && !strings.HasPrefix(name, ".claude-plugin/types/")
}

// walkInstallable calls fn with each installable embedded file, by its path
// within the mod.
func walkInstallable(fn func(rel string, b []byte) error) error {
	return fs.WalkDir(modFS, "mod", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, "mod/")
		if !installable(rel) {
			return nil
		}
		b, err := modFS.ReadFile(p)
		if err != nil {
			return err
		}
		return fn(rel, b)
	})
}

// modVersion identifies the embedded mod by its content, so any change to it
// reinstalls without a manual version bump.
var modVersion = sync.OnceValue(func() string {
	h := sha256.New()
	_ = walkInstallable(func(rel string, b []byte) error {
		fmt.Fprintf(h, "%s\x00%d\x00", rel, len(b))
		h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:12]
})

// writeMarketplace lays the embedded mod out as a one-plugin local marketplace.
func writeMarketplace(dir string) error {
	pluginDir := filepath.Join(dir, pluginName)
	if err := os.RemoveAll(pluginDir); err != nil {
		return err
	}
	err := walkInstallable(func(rel string, b []byte) error {
		if rel == ".claude-plugin/plugin.json" {
			var err error
			if b, err = withVersion(b, modVersion()); err != nil {
				return err
			}
		}
		dst := filepath.Join(pluginDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		return err
	}
	manifest, _ := json.MarshalIndent(map[string]any{
		"name":    marketplaceName,
		"owner":   map[string]string{"name": "argus"},
		"plugins": []map[string]string{{"name": pluginName, "source": "./" + pluginName}},
	}, "", "  ")
	mdir := filepath.Join(dir, ".claude-plugin")
	if err := os.MkdirAll(mdir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(mdir, "marketplace.json"), manifest, 0o644)
}

func withVersion(manifest []byte, version string) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(manifest, &m); err != nil {
		return nil, err
	}
	m["version"] = version
	return json.MarshalIndent(m, "", "  ")
}

func checkClaude(ctx context.Context) error {
	if !claudeOnPath() {
		return adapter.ErrAgentNotInstalled
	}
	out, err := runClaude(ctx, "", "--version")
	if err != nil {
		return err
	}
	have := strings.Fields(string(out))
	if len(have) == 0 || !versionAtLeast(have[0], minClaudeVersion) {
		return fmt.Errorf("the argus plugin needs Claude Code %s or later (found %q): upgrade Claude Code, then run `argus hooks install`", minClaudeVersion, strings.TrimSpace(string(out)))
	}
	return nil
}

// versionAtLeast compares dotted numeric versions; anything unparsable is too old.
func versionAtLeast(have, want string) bool {
	h, w := strings.Split(have, "."), strings.Split(want, ".")
	for i := range w {
		if i >= len(h) {
			return false
		}
		hn, err1 := strconv.Atoi(h[i])
		wn, err2 := strconv.Atoi(w[i])
		if err1 != nil || err2 != nil {
			return false
		}
		if hn != wn {
			return hn > wn
		}
	}
	return true
}

func findMarketplace(ctx context.Context) (path string, found bool, err error) {
	out, err := runClaude(ctx, "", "plugin", "marketplace", "list", "--json")
	if err != nil {
		return "", false, err
	}
	var list []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return "", false, fmt.Errorf("parse marketplace list: %w", err)
	}
	for _, m := range list {
		if m.Name == marketplaceName {
			return m.Path, true, nil
		}
	}
	return "", false, nil
}

// installedVersion returns the user-scope mod's version; ok is false when absent.
func installedVersion(ctx context.Context) (version string, ok bool, err error) {
	out, err := runClaude(ctx, "", "plugin", "list", "--json")
	if err != nil {
		return "", false, err
	}
	var list []struct {
		ID      string `json:"id"`
		Version string `json:"version"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return "", false, fmt.Errorf("parse plugin list: %w", err)
	}
	for _, p := range list {
		if p.ID == pluginID && p.Scope == "user" {
			return p.Version, true, nil
		}
	}
	return "", false, nil
}

// errNoModSocket refuses a fresh install that would leave the mod unable to reach argusd.
var errNoModSocket = errors.New("argus needs its mod socket path to install the Claude Code plugin")

// installMod removes argus's old hooks and installs or updates the embedded mod.
// The version check comes first, so an old Claude keeps its hooks, refreshed to
// opts.ArgusBin. An empty socket keeps an installed mod's configured one, but
// cannot install afresh.
func installMod(ctx context.Context, opts adapter.InstallOptions) error {
	unlock, err := lockMod()
	if err != nil {
		return err
	}
	defer unlock()
	if err := checkClaude(ctx); err != nil {
		_, err = refreshLegacyHooks(opts.ArgusBin, err)
		return err
	}
	version, installed, err := installedVersion(ctx)
	if err != nil {
		return err
	}
	return applyMod(ctx, opts.Socket, version, installed)
}

// applyMod is installMod once claude is checked, given the installed mod's
// version (installed false when absent).
func applyMod(ctx context.Context, socket, version string, installed bool) error {
	if socket == "" && !installed {
		// Checked before anything is removed, so the old hooks stay.
		return errNoModSocket
	}
	dir := marketplaceDir()
	if err := writeMarketplace(dir); err != nil {
		return err
	}
	path, found, err := findMarketplace(ctx)
	if err != nil {
		return err
	}
	switch {
	case found && path != dir:
		return fmt.Errorf("another Claude Code marketplace is named %q (%s); remove it with `claude plugin marketplace remove %s`, then run `argus hooks install`", marketplaceName, path, marketplaceName)
	case found && installed && version == modVersion():
		// The installed mod is current, so a marketplace update changes nothing.
	case found:
		_, err = runClaude(ctx, "", "plugin", "marketplace", "update", marketplaceName)
	default:
		_, err = runClaude(ctx, "", "plugin", "marketplace", "add", dir)
	}
	if err != nil {
		return err
	}
	switch {
	case !installed:
		_, err = runClaude(ctx, "", "plugin", "install", pluginID, "--config", "socket="+socket)
	case version != modVersion():
		_, err = runClaude(ctx, "", "plugin", "update", pluginID)
	}
	if err != nil {
		return err
	}
	if installed {
		if _, err := reapplySocket(ctx, socket); err != nil {
			return err
		}
	}
	// Only now: a failed step above keeps the old hooks and stamp, so the next
	// reconcile retries.
	if err := removeLegacyHooks(); err != nil {
		return err
	}
	return stampInstalled()
}

// reapplySocket points the installed mod at socket and reports whether it
// moved; an empty socket or the one already configured is left alone.
func reapplySocket(ctx context.Context, socket string) (bool, error) {
	if socket == "" || configuredSocket() == socket {
		return false, nil
	}
	return true, configureSocket(ctx, socket)
}

func configureSocket(ctx context.Context, socket string) error {
	values, _ := json.Marshal(map[string]string{"socket": socket})
	_, err := runClaude(ctx, string(values), "plugin", "configure", pluginID, "--values-stdin")
	return err
}

func socketLive(path string) bool {
	if path == "" {
		return false
	}
	c, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// configuredSocket is the socket option saved for the mod in settings.json, or "".
func configuredSocket() string {
	path, err := SettingsPath()
	if err != nil {
		return ""
	}
	top, err := readTop(path)
	if err != nil {
		return ""
	}
	var cfg map[string]struct {
		Options struct {
			Socket string `json:"socket"`
		} `json:"options"`
	}
	if json.Unmarshal(top["pluginConfigs"], &cfg) != nil {
		return ""
	}
	return cfg[pluginID].Options.Socket
}

// reconcileMod migrates old hooks to the mod, updates a stale mod and moves it
// to a new socket, but never installs for a user who opted into neither. Old
// hooks that must stay are refreshed to opts.ArgusBin.
func reconcileMod(ctx context.Context, opts adapter.InstallOptions) ([]string, error) {
	hooks, err := loadHooks()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	legacy := err == nil && spec().AnyManaged(hooks)
	socket, configured := opts.Socket, configuredSocket()
	if socket != configured && socketLive(configured) {
		// Another node serves the mod (a dev node on its own socket meets the
		// user's): leave the mod with it.
		socket = ""
	}
	if !legacy {
		if _, statErr := os.Stat(marketplaceDir()); errors.Is(statErr, fs.ErrNotExist) {
			return nil, nil
		}
		// The current mod was written and the socket stayed: skip the claude calls.
		if stampedVersion() == modVersion() && (socket == "" || configured == socket) {
			return nil, nil
		}
	}
	unlock, err := lockMod()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if !claudeOnPath() {
		if legacy {
			return refreshLegacyHooks(opts.ArgusBin, errors.New("claude is not on argusd's PATH, so the old settings hooks cannot be replaced with the argus mod; run `argus hooks install`"))
		}
		return nil, errors.New("claude is not on argusd's PATH, so the argus plugin cannot be updated; run `argus hooks install`")
	}
	if err := checkClaude(ctx); err != nil {
		return refreshLegacyHooks(opts.ArgusBin, err)
	}
	version, installed, err := installedVersion(ctx)
	if err != nil {
		return nil, err
	}
	if !legacy {
		if !installed {
			return nil, nil
		}
		if version == modVersion() {
			if moved, err := reapplySocket(ctx, socket); err != nil || !moved {
				return nil, err
			}
			return []string{"argus mod socket " + socket}, nil
		}
	}
	if err := applyMod(ctx, socket, version, installed); err != nil {
		return nil, err
	}
	return []string{"argus mod " + modVersion()}, nil
}

// lockMod holds an exclusive lock on the marketplace, so an install and a
// node's background reconcile do not rewrite it at once.
func lockMod() (func(), error) {
	path := marketplaceDir() + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}

// installedStamp, in the marketplace dir, holds the mod version argus last
// installed in full.
const installedStamp = ".installed"

func stampInstalled() error {
	return os.WriteFile(filepath.Join(marketplaceDir(), installedStamp), []byte(modVersion()), 0o644)
}

func stampedVersion() string {
	b, err := os.ReadFile(filepath.Join(marketplaceDir(), installedStamp))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// uninstallMod removes the mod, its marketplace and any old hooks.
func uninstallMod(ctx context.Context) error {
	unlock, err := lockMod()
	if err != nil {
		return err
	}
	defer unlock()
	if err := removeLegacyHooks(); err != nil {
		return err
	}
	if !claudeOnPath() {
		if _, err := os.Stat(marketplaceDir()); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		// Keep the marketplace: Claude would load a plugin whose source is gone.
		return errors.New("claude is not on PATH, so the argus plugin cannot be removed from Claude Code")
	}
	if _, installed, err := installedVersion(ctx); err != nil {
		return err
	} else if installed {
		if _, err := runClaude(ctx, "", "plugin", "uninstall", pluginID); err != nil {
			return err
		}
	}
	if path, found, err := findMarketplace(ctx); err != nil {
		return err
	} else if found && path == marketplaceDir() {
		if _, err := runClaude(ctx, "", "plugin", "marketplace", "remove", marketplaceName); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(marketplaceDir()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
