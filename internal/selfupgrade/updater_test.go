package selfupgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/config"
)

var newBinary = []byte("#!/bin/sh\necho new\n")

type fakeRelease struct {
	tag          string
	latestStatus atomic.Int32
	bin          []byte
	sum          string
	latestHit    atomic.Int32
	binHit       atomic.Int32
}

func newFakeRelease(t *testing.T, tag string, bin []byte) (*fakeRelease, *httptest.Server) {
	t.Helper()
	h := sha256.Sum256(bin)
	f := &fakeRelease{tag: tag, bin: bin, sum: hex.EncodeToString(h[:]) + "\n"}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+config.Repo+"/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		f.latestHit.Add(1)
		if code := f.latestStatus.Load(); code != 0 {
			w.WriteHeader(int(code))
			return
		}
		fmt.Fprintf(w, `{"tag_name":%q}`, f.tag)
	})
	mux.HandleFunc("/"+config.Repo+"/releases/download/", func(w http.ResponseWriter, r *http.Request) {
		asset := AssetName(f.tag, runtime.GOOS, runtime.GOARCH)
		switch filepath.Base(r.URL.Path) {
		case asset:
			f.binHit.Add(1)
			_, _ = w.Write(f.bin)
		case asset + ".sha256":
			_, _ = w.Write([]byte(f.sum))
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv
}

func testUpdater(t *testing.T, srv *httptest.Server, name string) *Updater {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, name)
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Updater{Version: "1.0.0", ExecPath: exe, APIBase: srv.URL, DLBase: srv.URL, Client: srv.Client()}
}

func assertNoTempLeft(t *testing.T, u *Updater) {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(filepath.Dir(u.ExecPath), ".argus-upgrade-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAssetName(t *testing.T) {
	if got := AssetName("0.0.9", "darwin", "arm64"); got != "argus-0.0.9-darwin-arm64" {
		t.Fatalf("AssetName = %q", got)
	}
}

func TestApplyReplacesBinary(t *testing.T) {
	_, srv := newFakeRelease(t, "1.1.0", newBinary)
	u := testUpdater(t, srv, "argus")
	from, to, err := u.Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if from != "1.0.0" || to != "1.1.0" {
		t.Fatalf("from %q to %q", from, to)
	}
	if got := readFile(t, u.ExecPath); got != string(newBinary) {
		t.Fatalf("binary not replaced: %q", got)
	}
	assertNoTempLeft(t, u)
}

func TestApplySkipsDownloadOfBinaryOnDisk(t *testing.T) {
	f, srv := newFakeRelease(t, "1.1.0", newBinary)
	u := testUpdater(t, srv, "argus")
	if err := os.WriteFile(u.ExecPath, newBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	from, to, err := u.Apply(context.Background())
	if err != nil || from != "1.0.0" || to != "1.1.0" {
		t.Fatalf("Apply = %q, %q, %v", from, to, err)
	}
	if n := f.binHit.Load(); n != 0 {
		t.Fatalf("downloaded the binary %d times, want 0", n)
	}
	assertNoTempLeft(t, u)
}

func TestApplyLeavesOtherTempPathsAlone(t *testing.T) {
	_, srv := newFakeRelease(t, "1.1.0", newBinary)
	u := testUpdater(t, srv, "argus")
	other := u.ExecPath + ".tmp"
	if err := os.WriteFile(other, []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := u.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := readFile(t, other); got != "other" {
		t.Fatalf("another download's temp file was clobbered: %q", got)
	}
}

func TestApplyRemovesStaleTempsOnly(t *testing.T) {
	_, srv := newFakeRelease(t, "1.1.0", newBinary)
	u := testUpdater(t, srv, "argus")
	dir := filepath.Dir(u.ExecPath)
	stale := filepath.Join(dir, ".argus-upgrade-stale")
	fresh := filepath.Join(dir, ".argus-upgrade-fresh")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if _, _, err := u.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale temp file not removed: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh temp file of a running upgrade removed: %v", err)
	}
}

func TestApplyAlreadyLatest(t *testing.T) {
	f, srv := newFakeRelease(t, "1.0.0", newBinary)
	u := testUpdater(t, srv, "argus")
	from, to, err := u.Apply(context.Background())
	if err != nil || from != "1.0.0" || to != "1.0.0" {
		t.Fatalf("Apply = %q, %q, %v", from, to, err)
	}
	if f.binHit.Load() != 0 {
		t.Fatal("must not download when already on the latest version")
	}
}

func TestApplyKeepsBuildPastLatestTag(t *testing.T) {
	f, srv := newFakeRelease(t, "1.0.0", newBinary)
	u := testUpdater(t, srv, "argus")
	u.Version = "1.0.0-14-g0cac2131"
	from, to, err := u.Apply(context.Background())
	if err != nil || from != u.Version || to != u.Version {
		t.Fatalf("Apply = %q, %q, %v", from, to, err)
	}
	if f.binHit.Load() != 0 {
		t.Fatal("must not downgrade a build made after the latest tag")
	}
}

func TestApplyTwiceDownloadsOnce(t *testing.T) {
	f, srv := newFakeRelease(t, "1.1.0", newBinary)
	u := testUpdater(t, srv, "argus")
	if _, _, err := u.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	from, to, err := u.Apply(context.Background())
	if err != nil || from != "1.0.0" || to != "1.1.0" {
		t.Fatalf("second Apply = %q, %q, %v; want 1.0.0, 1.1.0 so that the caller restarts", from, to, err)
	}
	if n := f.binHit.Load(); n != 1 {
		t.Fatalf("binary downloaded %d times, want 1", n)
	}
}

func TestApplyChecksumMismatch(t *testing.T) {
	f, srv := newFakeRelease(t, "1.1.0", newBinary)
	f.sum = strings.Repeat("0", 64) + "  whatever\n"
	u := testUpdater(t, srv, "argus")
	_, _, err := u.Apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
	if got := readFile(t, u.ExecPath); !strings.Contains(got, "old") {
		t.Fatal("old binary must stay after a checksum mismatch")
	}
	assertNoTempLeft(t, u)
}

func TestApplyMalformedChecksum(t *testing.T) {
	f, srv := newFakeRelease(t, "1.1.0", newBinary)
	f.sum = "not-a-hash\n"
	u := testUpdater(t, srv, "argus")
	if _, _, err := u.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "malformed checksum") {
		t.Fatalf("err = %v, want malformed checksum", err)
	}
	if got := readFile(t, u.ExecPath); !strings.Contains(got, "old") {
		t.Fatal("old binary must stay after a malformed checksum")
	}
	assertNoTempLeft(t, u)
}

func TestApplyVersionCheckFails(t *testing.T) {
	_, srv := newFakeRelease(t, "1.1.0", []byte("#!/bin/sh\nexit 3\n"))
	u := testUpdater(t, srv, "argus")
	if _, _, err := u.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("err = %v, want verification failed", err)
	}
	if got := readFile(t, u.ExecPath); !strings.Contains(got, "old") {
		t.Fatal("old binary must stay")
	}
	assertNoTempLeft(t, u)
}

func TestApplyRefusesOtherBinaryName(t *testing.T) {
	_, srv := newFakeRelease(t, "1.1.0", newBinary)
	u := testUpdater(t, srv, "go-build-123")
	if _, _, err := u.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "only be run with the argus binary") {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyUnwritableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	_, srv := newFakeRelease(t, "1.1.0", newBinary)
	u := testUpdater(t, srv, "argus")
	dir := filepath.Dir(u.ExecPath)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_, _, err := u.Apply(context.Background())
	want := "cannot write " + dir + ": permission denied (argus must be able to replace its own binary)"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestApplyBusy(t *testing.T) {
	_, srv := newFakeRelease(t, "1.1.0", newBinary)
	u := testUpdater(t, srv, "argus")
	if !u.BeginRestart() {
		t.Fatal("BeginRestart on an idle updater must succeed")
	}
	if _, _, err := u.Apply(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestCheckCachesLatest(t *testing.T) {
	f, srv := newFakeRelease(t, "1.1.0", newBinary)
	u := testUpdater(t, srv, "argus")
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	u.now = func() time.Time { return now }
	for range 2 {
		c, err := u.Check(context.Background())
		if err != nil || c.Current != "1.0.0" || c.Latest != "1.1.0" {
			t.Fatalf("Check = %+v, %v", c, err)
		}
	}
	if n := f.latestHit.Load(); n != 1 {
		t.Fatalf("latest fetched %d times within the cache window, want 1", n)
	}
	now = now.Add(11 * time.Minute)
	if _, err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.latestHit.Load(); n != 2 {
		t.Fatalf("latest fetched %d times after the cache window, want 2", n)
	}
}

func TestApplyIgnoresCachedLatest(t *testing.T) {
	f, srv := newFakeRelease(t, "1.1.0", newBinary)
	u := testUpdater(t, srv, "argus")
	if _, err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.tag = "1.2.0"
	_, to, err := u.Apply(context.Background())
	if err != nil || to != "1.2.0" {
		t.Fatalf("Apply to %q, %v; want 1.2.0", to, err)
	}
	c, err := u.Check(context.Background())
	if err != nil || c.Latest != "1.2.0" {
		t.Fatalf("Check after Apply = %+v, %v", c, err)
	}
}

func TestCheckLatestFailureCachesNothing(t *testing.T) {
	for name, breakRelease := range map[string]func(*fakeRelease){
		"non-200":        func(f *fakeRelease) { f.latestStatus.Store(http.StatusInternalServerError) },
		"empty tag_name": func(f *fakeRelease) { f.tag = "" },
	} {
		t.Run(name, func(t *testing.T) {
			f, srv := newFakeRelease(t, "1.1.0", newBinary)
			u := testUpdater(t, srv, "argus")
			breakRelease(f)
			if c, err := u.Check(context.Background()); err == nil {
				t.Fatalf("Check = %+v, want an error", c)
			}
			f.tag = "1.1.0"
			f.latestStatus.Store(0)
			c, err := u.Check(context.Background())
			if err != nil || c.Latest != "1.1.0" {
				t.Fatalf("Check after recovery = %+v, %v", c, err)
			}
			if n := f.latestHit.Load(); n != 2 {
				t.Fatalf("latest fetched %d times, want 2 (a failure must not be cached)", n)
			}
		})
	}
}

func fakeGH(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestFetchAssetRejectsOversizedChecksum(t *testing.T) {
	big := strings.Repeat("a", maxChecksumSize+1)
	t.Run("gh", func(t *testing.T) {
		fakeGH(t, "printf %s "+big+"\n")
		u := &Updater{UseGH: true}
		if _, err := u.fetchAsset(context.Background(), "1.1.0", "x.sha256"); err == nil || !strings.Contains(err.Error(), "too large") {
			t.Fatalf("err = %v, want too large", err)
		}
	})
	t.Run("http", func(t *testing.T) {
		f, srv := newFakeRelease(t, "1.1.0", newBinary)
		f.sum = big
		u := testUpdater(t, srv, "argus")
		asset := AssetName("1.1.0", runtime.GOOS, runtime.GOARCH) + ".sha256"
		if _, err := u.fetchAsset(context.Background(), "1.1.0", asset); err == nil || !strings.Contains(err.Error(), "too large") {
			t.Fatalf("err = %v, want too large", err)
		}
	})
}

func TestApplyFallsBackToHTTPWhenGHFails(t *testing.T) {
	fakeGH(t, "echo 'not logged in' >&2; exit 1\n")
	_, srv := newFakeRelease(t, "1.1.0", newBinary)
	u := testUpdater(t, srv, "argus")
	u.UseGH = true
	if _, to, err := u.Apply(context.Background()); err != nil || to != "1.1.0" {
		t.Fatalf("Apply to %q, %v", to, err)
	}
	if got := readFile(t, u.ExecPath); got != string(newBinary) {
		t.Fatalf("binary not replaced: %q", got)
	}
}

func TestCheckReportsBothErrorsWhenGHAndHTTPFail(t *testing.T) {
	fakeGH(t, "echo 'not logged in' >&2; exit 1\n")
	f, srv := newFakeRelease(t, "1.1.0", newBinary)
	f.latestStatus.Store(http.StatusInternalServerError)
	u := testUpdater(t, srv, "argus")
	u.UseGH = true
	_, err := u.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("err = %v, want both the HTTP and the gh error", err)
	}
}
