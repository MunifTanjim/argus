package codex

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// From ~/.codex/shell_snapshots.
type snapshot struct {
	threadID   string
	startNS    int64
	paneID     string // $TMUX_PANE, e.g. "%73"; "" when not under tmux
	socketPath string // $TMUX socket path (before the ",pid,grp" suffix); "" when not under tmux
}

func snapshotsDir() (string, error) {
	dir, err := codexHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "shell_snapshots"), nil
}

// Newest start_ns wins on duplicates.
func listSnapshots() ([]snapshot, error) {
	dir, err := snapshotsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	byThread := map[string]snapshot{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		s, ok := parseSnapshotName(e.Name())
		if !ok {
			continue
		}
		if prev, ok := byThread[s.threadID]; ok && prev.startNS >= s.startNS {
			continue
		}
		s.paneID, s.socketPath = readSnapshotTmux(filepath.Join(dir, e.Name()))
		byThread[s.threadID] = s
	}

	out := make([]snapshot, 0, len(byThread))
	for _, s := range byThread {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].threadID < out[j].threadID })
	return out, nil
}

// Format: <thread_id>.<start_ns>.sh
func parseSnapshotName(name string) (snapshot, bool) {
	base := strings.TrimSuffix(name, ".sh")
	threadID, rest, ok := strings.Cut(base, ".")
	if !ok || threadID == "" {
		return snapshot{}, false
	}
	ns, _ := strconv.ParseInt(rest, 10, 64) // best-effort; 0 is a fine tie-breaker
	return snapshot{threadID: threadID, startNS: ns}, true
}

func readSnapshotTmux(path string) (paneID, socketPath string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // snapshots hold long shell-function lines
	for sc.Scan() {
		line := strings.TrimPrefix(strings.TrimSpace(sc.Text()), "export ")
		if v, ok := strings.CutPrefix(line, "TMUX_PANE="); ok {
			paneID = unquote(v)
		} else if v, ok := strings.CutPrefix(line, "TMUX="); ok {
			// $TMUX is "<socket>,<pid>,<group>"; keep only the socket path.
			socketPath, _, _ = strings.Cut(unquote(v), ",")
		}
		if paneID != "" && socketPath != "" {
			break
		}
	}
	return paneID, socketPath
}

func unquote(v string) string {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), ";"))
	if len(v) >= 2 {
		if (v[0] == '\'' && v[len(v)-1] == '\'') || (v[0] == '"' && v[len(v)-1] == '"') {
			return v[1 : len(v)-1]
		}
	}
	return v
}
