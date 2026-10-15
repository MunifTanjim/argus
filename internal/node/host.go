package node

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/host"
)

// SetWakelockPath persists the wakelock at path so it survives a restart.
// Call before Run.
func (d *Node) SetWakelockPath(path string) { d.wakelock = host.NewWakelock(path) }

func (d *Node) handleHostInfo(ctx context.Context, _ json.RawMessage) (any, error) {
	uptime, err := host.Uptime()
	if err != nil {
		return nil, err
	}
	battery, err := host.Battery(ctx)
	if err != nil {
		return nil, err
	}
	return api.HostInfo{OS: host.OS(), UptimeSeconds: uptime, Battery: battery, Wakelock: d.wakelock.State()}, nil
}

func (d *Node) handleHostSetWakelock(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.HostSetWakelockParams](params)
	if err != nil {
		return nil, err
	}
	w, err := d.wakelock.Set(p.Until)
	if err != nil {
		return nil, wakelockRPCError(err)
	}
	return w, nil
}

func wakelockRPCError(err error) error {
	code := api.CodeInternalError
	if errors.Is(err, host.ErrInvalidUntil) || errors.Is(err, host.ErrWakelockUnsupported) {
		code = api.CodeInvalidRequest
	}
	return &api.RPCError{Code: code, Message: err.Error()}
}

// completeDirLimit bounds a completion reply for a directory with many children.
const completeDirLimit = 200

func (d *Node) handleHostCompleteDir(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.HostCompleteDirParams](params)
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	res := api.HostCompleteDirResult{Home: home, Paths: []string{}}
	typed := p.Path
	switch {
	case typed == "" && home != "":
		typed = strings.TrimSuffix(home, "/") + "/"
	case typed == "~":
		typed = "~/"
	}
	path := expandHome(typed, home)
	if !filepath.IsAbs(path) {
		return res, nil
	}
	i := strings.LastIndexByte(path, '/')
	dir, prefix := path[:i+1], path[i+1:]
	parent := typed[:strings.LastIndexByte(typed, '/')+1]
	entries, err := os.ReadDir(dir)
	if err != nil {
		return res, nil // a missing or unreadable parent just has no completions
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		if !e.IsDir() {
			if e.Type()&fs.ModeSymlink == 0 {
				continue
			}
			if fi, err := os.Stat(filepath.Join(dir, name)); err != nil || !fi.IsDir() {
				continue
			}
		}
		res.Paths = append(res.Paths, parent+name+"/")
		if len(res.Paths) == completeDirLimit {
			break
		}
	}
	return res, nil
}

func expandHome(path, home string) string {
	if home == "" || path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	return home + path[1:]
}
