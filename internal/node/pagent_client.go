package node

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/atomicfile"
)

const (
	// clientAttachMin is how long a client connection must stay open to count
	// as attached; one-shot commands close sooner.
	clientAttachMin = 10 * time.Second
	// clientSaveEvery bounds how often the last-attached time is written while
	// a client stays attached.
	clientSaveEvery = 10 * time.Minute
)

// markClient records that the connection serving ctx is a client (a TUI or
// app listing sessions). Hook calls and the mod bridge never list sessions, so
// a long-held hook connection does not count as a user being present.
func (d *Node) markClient(ctx context.Context) {
	n, ok := api.NotifierFrom(ctx)
	if !ok {
		return
	}
	d.subsMu.Lock()
	if cs := d.conns[n]; cs != nil {
		cs.client = true
	}
	d.subsMu.Unlock()
}

// clientAttached reports whether a client connection has been open for at
// least clientAttachMin.
func (d *Node) clientAttached(now time.Time) bool {
	d.subsMu.Lock()
	defer d.subsMu.Unlock()
	for _, cs := range d.conns {
		if cs != nil && cs.client && !cs.opened.IsZero() && now.Sub(cs.opened) >= clientAttachMin {
			return true
		}
	}
	return false
}

// connClosed records a client that stayed attached until now.
func (d *Node) connClosed(cs *connSubs) {
	now := d.pagentNow()
	d.subsMu.Lock()
	client, opened := cs.client, cs.opened
	d.subsMu.Unlock()
	if client && !opened.IsZero() && now.Sub(opened) >= clientAttachMin {
		d.clientSeen(now, true)
	}
}

// clientSeen moves the last-attached time to now, writing it to the state
// file when force is set or clientSaveEvery has passed since the last write.
func (d *Node) clientSeen(now time.Time, force bool) {
	d.clientMu.Lock()
	defer d.clientMu.Unlock()
	d.lastClientAt = now
	if d.lastClientFile == "" || (!force && now.Sub(d.lastClientSaved) < clientSaveEvery) {
		return
	}
	d.lastClientSaved = now
	if err := os.MkdirAll(filepath.Dir(d.lastClientFile), 0o755); err == nil {
		if err := atomicfile.Write(d.lastClientFile, []byte(strconv.FormatInt(now.Unix(), 10))); err != nil {
			d.log.Warn("pagent: save last client", "err", err)
		}
	}
}

// lastClient returns when a client was last attached. With no record it is
// the first call, so the absence clock starts when the node does.
func (d *Node) lastClient(now time.Time) time.Time {
	if d.clientAttached(now) {
		d.clientSeen(now, false)
		return now
	}
	d.clientMu.Lock()
	t := d.lastClientAt
	if t.IsZero() && d.lastClientFile != "" {
		if data, err := os.ReadFile(d.lastClientFile); err == nil {
			if n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil {
				t = time.Unix(n, 0)
				d.lastClientAt = t
			}
		}
	}
	d.clientMu.Unlock()
	if t.IsZero() {
		d.clientSeen(now, true)
		return now
	}
	return t
}

// clientAway reports whether no client has been attached for the
// pause-after-away limit.
func (d *Node) clientAway(now time.Time) bool {
	limit := d.pagentCfg.Defaults.Limits.PauseAfterAway
	if limit <= 0 {
		return false
	}
	return now.Sub(d.lastClient(now)) >= limit
}
