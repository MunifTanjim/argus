// Package selfupgrade replaces the running argus binary with the latest release.
package selfupgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/shell"
	"github.com/MunifTanjim/argus/internal/util"
)

var ErrBusy = errors.New("an upgrade or restart is already in progress")

const checkCacheTTL = 10 * time.Minute

const (
	tempPrefix = ".argus-upgrade-"
	// staleTempAge is far beyond any download budget, so a temp file this old
	// belongs to no running upgrade, even one from another daemon that shares
	// the binary.
	staleTempAge = time.Hour
)

type Updater struct {
	Version  string // the running version
	ExecPath string
	UseGH    bool
	APIBase  string
	DLBase   string
	Client   *http.Client

	busy sync.Mutex
	// installed is the tag already swapped in, which the running process does
	// not run until it restarts.
	installed string

	cacheMu  sync.Mutex
	latest   string
	latestAt time.Time
	now      func() time.Time
}

// New returns an Updater for the running executable.
func New(version string) (*Updater, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to get executable path: %w", err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return nil, fmt.Errorf("failed to resolve executable path: %w", err)
	}
	return &Updater{
		Version:  version,
		ExecPath: exe,
		UseGH:    util.HasTool("gh"),
		APIBase:  "https://api.github.com",
		DLBase:   "https://github.com",
		Client:   &http.Client{Timeout: Timeout},
	}, nil
}

type Check struct {
	Current string
	Latest  string
}

func (u *Updater) Check(ctx context.Context) (Check, error) {
	tag, err := u.latestTag(ctx, checkCacheTTL)
	if err != nil {
		return Check{}, err
	}
	return Check{Current: u.Version, Latest: tag}, nil
}

func (u *Updater) latestTag(ctx context.Context, maxAge time.Duration) (string, error) {
	u.cacheMu.Lock()
	defer u.cacheMu.Unlock()
	if u.latest == "" || u.clock().Sub(u.latestAt) >= maxAge {
		tag, err := u.fetchLatestTag(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to resolve latest release: %w", err)
		}
		u.latest, u.latestAt = tag, u.clock()
	}
	return u.latest, nil
}

// Apply swaps in the latest release. It does not restart. to == from means
// the process already runs the latest version.
func (u *Updater) Apply(ctx context.Context) (from, to string, err error) {
	if !u.busy.TryLock() {
		return "", "", ErrBusy
	}
	defer u.busy.Unlock()
	if filepath.Base(u.ExecPath) != config.BinaryName {
		return "", "", fmt.Errorf("upgrade can only be run with the %s binary", config.BinaryName)
	}
	tag, err := u.latestTag(ctx, 0)
	if err != nil {
		return "", "", err
	}
	if tag == u.Version || tag == u.installed {
		return u.Version, tag, nil
	}
	if err := u.install(ctx, tag); err != nil {
		return "", "", err
	}
	u.installed = tag
	return u.Version, tag, nil
}

// BeginRestart takes the lock that Apply takes and never releases it: the
// process is about to exec, and no upgrade may start in the meantime.
func (u *Updater) BeginRestart() bool { return u.busy.TryLock() }

func (u *Updater) install(ctx context.Context, tag string) error {
	asset := AssetName(tag, runtime.GOOS, runtime.GOARCH)
	sum, err := u.fetchAsset(ctx, tag, asset+".sha256")
	if err != nil {
		return fmt.Errorf("failed to download checksum: %w", err)
	}
	// Another daemon that shares the binary can have swapped it in already.
	if verifyChecksum(u.ExecPath, sum, asset) == nil {
		return nil
	}
	dir := filepath.Dir(u.ExecPath)
	u.removeStaleTemps(dir)
	f, err := os.CreateTemp(dir, tempPrefix+"*")
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("cannot write %s: permission denied (argus must be able to replace its own binary)", dir)
	}
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmp := f.Name()
	f.Close()
	if err := u.stage(ctx, tag, asset, sum, tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (u *Updater) removeStaleTemps(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), tempPrefix) {
			continue
		}
		if info, err := e.Info(); err == nil && u.clock().Sub(info.ModTime()) > staleTempAge {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func (u *Updater) stage(ctx context.Context, tag, asset string, sum []byte, tmp string) error {
	if err := u.downloadAsset(ctx, tag, asset, tmp); err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	if err := verifyChecksum(tmp, sum, asset); err != nil {
		return err
	}
	if err := shell.NewCommandContext(ctx, tmp, "--version").Run(); err != nil {
		return fmt.Errorf("downloaded binary verification failed: %w", err)
	}
	if err := os.Rename(tmp, u.ExecPath); err != nil {
		return fmt.Errorf("failed to replace binary: %w", err)
	}
	return nil
}

func verifyChecksum(path string, sumFile []byte, asset string) error {
	fields := strings.Fields(string(sumFile))
	if len(fields) == 0 || len(fields[0]) != sha256.Size*2 {
		return fmt.Errorf("malformed checksum file for %s", asset)
	}
	want := strings.ToLower(fields[0])
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset, got, want)
	}
	return nil
}

func (u *Updater) clock() time.Time {
	if u.now != nil {
		return u.now()
	}
	return time.Now()
}
