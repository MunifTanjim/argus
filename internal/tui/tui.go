// Package tui is argus's Bubble Tea terminal client: session list, live registry
// events, transcript view, and screen passthrough for direct pane interaction.
package tui

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bundle"
	"github.com/MunifTanjim/argus/internal/logbuf"
)

// Client is the connection the TUI drives. *api.ReconnectingClient also satisfies
// it, surfacing connection transitions on States() and re-dialing on Reconnect().
type Client interface {
	Call(method string, params, out any) error
	Events() <-chan api.Notification
	States() <-chan bool
	Reconnect()
	Close() error
}

// Run connects the TUI and blocks until the user quits. Non-nil logs (embedded
// node) are tailed in the Logs tab.
func Run(client Client, logs *logbuf.Buffer) error {
	// Detect background ONCE: the OSC 11 query can fail once alt-screen is active.
	hasDark := lipgloss.HasDarkBackground(os.Stdin, os.Stderr)
	initTheme(hasDark)
	initIcons()
	initStyles()

	m := newModel(client, hasDark, logs)
	go sendTermKeyLoop(client, m.termKeyCh) // single ordered sender for live-terminal input
	p := tea.NewProgram(m)
	go func() {
		events, states := client.Events(), client.States()
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					return // plain (non-reconnecting) client: connection ended
				}
				p.Send(notificationMsg(ev))
			case connected, ok := <-states:
				if !ok {
					return
				}
				p.Send(connStateMsg{connected: connected})
			}
		}
	}()
	if logs != nil {
		go func() {
			for range logs.Notify() {
				p.Send(logTickMsg{})
			}
		}()
	}
	_, err := p.Run()
	return err
}

// newViewerModel builds a model seeded to display the single exported session
// from an offline file-backed client, opened directly in the transcript view.
func newViewerModel(client *fileClient, hasDark bool) model {
	m := newModel(client, hasDark, nil)
	m.viewer = true
	m.mode = modeHistoryTranscript
	m.history.openNodeID = ""
	m.history.openPath = client.entryPath
	m.history.openAgent = client.manifest.Agent
	m.history.openSession = client.syntheticSession()
	m.history.project = client.syntheticProject()
	return m
}

// RunBundle extracts and validates the bundle before launching the TUI, so a bad
// bundle errors out without ever opening the alt-screen.
func RunBundle(bundlePath string, redact bool) error {
	pruneOldExtractions()

	dest, err := bundleExtractDir(bundlePath)
	if err != nil {
		return err
	}

	m, err := bundle.ReadManifest(dest)
	if err != nil {
		// Unpack into a fresh temp dir, then atomically swap it in so a crash
		// mid-extract can't leave a partial dir a later run would reuse.
		if m, err = extractBundle(bundlePath, dest); err != nil {
			return err
		}
	}

	client, err := newFileClient(dest, m)
	if err != nil {
		return err
	}
	return RunViewer(client, bundlePath, redact)
}

// extractPrefix marks argus view's extraction and temp dirs in TempDir so
// pruneOldExtractions can find them.
const extractPrefix = "argus-view-"

// extractionMaxAge is not refreshed on reuse, so a still-wanted bundle past this
// age is re-extracted on the next view.
const extractionMaxAge = 48 * time.Hour

// pruneOldExtractions bounds TempDir growth from viewing many distinct bundles.
func pruneOldExtractions() {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-extractionMaxAge)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), extractPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(os.TempDir(), e.Name()))
	}
}

// bundleExtractDir keys the extraction dir by content hash so a renamed or copied
// bundle reuses its extraction. bundleHash samples only size + head/tail, so a
// mid-file same-size edit can alias an older one — fine here, bundles are
// regenerated whole, not edited in place.
func bundleExtractDir(bundlePath string) (string, error) {
	f, err := os.Open(bundlePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h, err := bundleHash(f)
	if err != nil {
		return "", err
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s%016x", extractPrefix, h)), nil
}

// bundleHashChunk is the head/tail block size sampled by bundleHash.
const bundleHashChunk = 65536

// bundleHash hashes a file by size plus its head and tail chunks (whole file when
// smaller than a chunk).
func bundleHash(f *os.File) (uint64, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	sum := uint64(size)
	if size < bundleHashChunk {
		buf := make([]byte, size)
		if _, err := io.ReadFull(f, buf); err != nil {
			return 0, err
		}
		return sum + sumWords(buf), nil
	}
	buf := make([]byte, bundleHashChunk)
	if _, err := f.ReadAt(buf, 0); err != nil {
		return 0, err
	}
	sum += sumWords(buf)
	if _, err := f.ReadAt(buf, size-bundleHashChunk); err != nil {
		return 0, err
	}
	return sum + sumWords(buf), nil
}

func sumWords(b []byte) uint64 {
	var sum uint64
	for len(b) >= 8 {
		sum += binary.LittleEndian.Uint64(b)
		b = b[8:]
	}
	if len(b) > 0 { // trailing <8 bytes (small-file path): pad so they still count
		var tail [8]byte
		copy(tail[:], b)
		sum += binary.LittleEndian.Uint64(tail[:])
	}
	return sum
}

func extractBundle(bundlePath, dest string) (bundle.Manifest, error) {
	f, err := os.Open(bundlePath)
	if err != nil {
		return bundle.Manifest{}, err
	}
	defer f.Close()

	tmp, err := os.MkdirTemp(os.TempDir(), extractPrefix+"tmp-*")
	if err != nil {
		return bundle.Manifest{}, err
	}
	m, err := bundle.Read(f, tmp)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return bundle.Manifest{}, err
	}
	_ = os.RemoveAll(dest) // clear any stale/partial dir before swapping in
	if err := os.Rename(tmp, dest); err != nil {
		// Lost a race with a concurrent extraction, or a cross-device rename: an
		// existing dest is a valid extraction, so drop ours and reuse it.
		_ = os.RemoveAll(tmp)
		if rm, rerr := bundle.ReadManifest(dest); rerr == nil {
			return rm, nil
		}
		return bundle.Manifest{}, err
	}
	return m, nil
}

// RunViewer runs the TUI as an offline viewer over an extracted .argus bundle,
// opening directly in the transcript view.
func RunViewer(client *fileClient, bundlePath string, redact bool) error {
	hasDark := lipgloss.HasDarkBackground(os.Stdin, os.Stderr)
	initTheme(hasDark)
	initIcons()
	initStyles()
	m := newViewerModel(client, hasDark)
	m.redactMode = redact
	m.bundlePath = bundlePath
	m.redactSrcDir = client.destDir
	p := tea.NewProgram(m)
	_, err := p.Run()
	cerr := client.Close()
	if err != nil {
		return err
	}
	return cerr
}
