package node

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/atomicfile"
)

const (
	// clientAttachMin is how long a connection must stay open to count as an
	// attached client; hook calls and one-shot commands close sooner.
	clientAttachMin = 10 * time.Second
	// clientSaveEvery bounds how often the last-attached time is written while
	// a client stays attached.
	clientSaveEvery = 10 * time.Minute
)

// clientAttached reports whether a client connection has been open for at
// least clientAttachMin.
func (d *Node) clientAttached(now time.Time) bool {
	d.subsMu.Lock()
	defer d.subsMu.Unlock()
	for _, cs := range d.conns {
		if cs != nil && !cs.opened.IsZero() && now.Sub(cs.opened) >= clientAttachMin {
			return true
		}
	}
	return false
}

// connClosed records a client that stayed attached until now.
func (d *Node) connClosed(opened time.Time) {
	now := d.pagentNow()
	if !opened.IsZero() && now.Sub(opened) >= clientAttachMin {
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
