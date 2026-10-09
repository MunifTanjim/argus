package node

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/spawn"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// termKeyPrefix keys a terminal in sessionTerms apart from session ids.
const termKeyPrefix = "term:"

func (d *Node) terminalClient() (*tmux.Client, error) {
	c, ok := d.clients[session.TmuxServerArgus]
	if !ok || !d.caps.Terminal {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "terminals not supported on this node"}
	}
	return c, nil
}

// terminalWindow refuses any window outside the terminal session: agent sessions share
// the argus server, and kill, rename, and link accept any window id.
func (d *Node) terminalWindow(ctx context.Context, c *tmux.Client, id string) (tmux.Window, error) {
	ws, err := c.ListWindows(ctx, spawn.TerminalSession)
	if err != nil {
		return tmux.Window{}, err
	}
	for _, w := range ws {
		if w.ID == id {
			return w, nil
		}
	}
	return tmux.Window{}, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown terminal: " + id}
}

func (d *Node) terminalAttached(id string) bool {
	d.sessionTermsMu.Lock()
	defer d.sessionTermsMu.Unlock()
	_, ok := d.sessionTerms[termKeyPrefix+id]
	return ok
}

func (d *Node) terminalOf(w tmux.Window, home string) api.Terminal {
	t := api.Terminal{
		ID: w.ID, Cwd: tildeHome(w.CurrentPath, home), Command: w.CurrentCommand,
		Attached: d.terminalAttached(w.ID), WorkspaceID: w.WorkspaceID,
	}
	if !w.AutoRename {
		t.Name = w.Name
	}
	return t
}

func tildeHome(p, home string) string {
	switch {
	case home == "":
		return p
	case p == home:
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, home+"/"); ok {
		return "~/" + rest
	}
	return p
}

func (d *Node) handleTerminalList(ctx context.Context, _ json.RawMessage) (any, error) {
	res := api.TerminalListResult{Terminals: []api.Terminal{}}
	c, err := d.terminalClient()
	if err != nil {
		return res, nil
	}
	ws, err := c.ListWindows(ctx, spawn.TerminalSession)
	if err != nil {
		return nil, err
	}
	// tmux reuses the lowest free window index, so index order is not creation
	// order; window ids only grow within a server.
	slices.SortFunc(ws, func(a, b tmux.Window) int { return cmp.Compare(windowSeq(a.ID), windowSeq(b.ID)) })
	home, _ := os.UserHomeDir()
	for _, w := range ws {
		res.Terminals = append(res.Terminals, d.terminalOf(w, home))
	}
	return res, nil
}

func (d *Node) handleTerminalCreate(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.TerminalCreateParams](params)
	if err != nil {
		return nil, err
	}
	c, err := d.terminalClient()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := home
	if p.WorkspaceID != "" {
		if dir, err = d.workspaceDir(ctx, p.WorkspaceID); err != nil {
			return nil, err
		}
		// tmux silently opens $HOME for a missing directory.
		if !dirExists(dir) {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "workspace directory is gone: " + dir}
		}
	}
	id, err := c.NewWindow(ctx, spawn.TerminalSession, dir)
	if err != nil {
		return nil, err
	}
	if p.WorkspaceID != "" {
		if err := c.SetWindowWorkspaceID(ctx, id, p.WorkspaceID); err != nil {
			_ = c.KillWindow(ctx, id)
			return nil, err
		}
	}
	d.notifyTerminalsChanged()
	w, err := d.terminalWindow(ctx, c, id)
	if err != nil {
		return nil, err
	}
	return d.terminalOf(w, home), nil
}

func (d *Node) handleTerminalKill(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.TerminalRef](params)
	if err != nil {
		return nil, err
	}
	c, err := d.terminalClient()
	if err != nil {
		return nil, err
	}
	if _, err := d.terminalWindow(ctx, c, p.TerminalID); err != nil {
		return nil, err
	}
	if err := c.KillWindow(ctx, p.TerminalID); err != nil {
		return nil, err
	}
	d.notifyTerminalsChanged()
	return nil, nil
}

func (d *Node) handleTerminalRename(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.TerminalRenameParams](params)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "name required"}
	}
	c, err := d.terminalClient()
	if err != nil {
		return nil, err
	}
	if _, err := d.terminalWindow(ctx, c, p.TerminalID); err != nil {
		return nil, err
	}
	if err := c.RenameWindow(ctx, p.TerminalID, name); err != nil {
		return nil, err
	}
	d.notifyTerminalsChanged()
	return nil, nil
}

func windowSeq(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "@"))
	return n
}
