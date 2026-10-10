package codex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// childThread is the thread a spawn_agent call created.
type childThread struct{ id, nickname, rollout string }

// Links never change once found, so they are cached for the process lifetime,
// per sessions root (a bundle resolves within itself, not the live home).
// Misses are not cached: a child's rollout may not be written yet.
var (
	linkMu      sync.Mutex
	spawnLinks  = map[spawnKey]childThread{} // (root, parent thread, spawn call id) -> child
	rolloutByID = map[threadKey]string{}     // (root, child thread id) -> rollout path
)

type (
	spawnKey  struct{ root, parent, callID string }
	threadKey struct{ root, id string }
)

// isLiveRoot reports whether sessionsRoot is the live Codex home, the only one
// the daemon and the state DB describe. Empty means the live home.
func isLiveRoot(sessionsRoot string) bool {
	if sessionsRoot == "" {
		return true
	}
	dir, err := codexHome()
	return err == nil && filepath.Clean(sessionsRoot) == filepath.Join(dir, "sessions")
}

// activeDiscoverer is the live daemon client, if any; subagent lookups ask the
// daemon before Codex's state DB.
var activeDiscoverer atomic.Pointer[discoverer]

const daemonLookupTimeout = time.Second

func daemonConn() *rpcConn {
	if d := activeDiscoverer.Load(); d != nil {
		return d.conn()
	}
	return nil
}

// subagentLinker resolves spawn_agent entries of one thread to their children.
// It opens Codex's state DB at most once and only when the daemon cannot answer.
type subagentLinker struct {
	parent       string
	sessionsRoot string
	turns        map[string]string // spawn call id -> turn id, for the daemon lookup

	db      *sql.DB
	dbTried bool
}

func (l *subagentLinker) close() {
	if l.db != nil {
		l.db.Close()
	}
}

func (l *subagentLinker) stateDB() *sql.DB {
	if !l.dbTried && isLiveRoot(l.sessionsRoot) {
		l.dbTried = true
		if p, err := stateDBPath(); err == nil {
			if _, err := os.Stat(p); err == nil {
				l.db, _ = openCodexDB(p)
			}
		}
	}
	return l.db
}

// stamp links each spawn_agent entry to its child and stamps the child's status
// and whether its rollout exists.
func (l *subagentLinker) stamp(entries []transcript.Entry) {
	var statuses map[string]string
	for i := range entries {
		e := &entries[i]
		if baseToolName(e.ToolName) != "spawn_agent" || len(e.Subagents) == 0 {
			continue
		}
		sub := &e.Subagents[0]
		if c, ok := l.child(e.ToolID, sub.ID, e.Result); ok {
			sub.ID = c.id
			if sub.Name == "" {
				sub.Name = c.nickname
			}
			sub.HasTrace = c.rollout != ""
		}
		if sub.ID == "" {
			continue
		}
		if statuses == nil {
			statuses = spawnStatuses(l.stateDB(), l.parent)
		}
		// A closed spawn edge (closed or shut down) outranks the activity status;
		// an open edge says nothing about progress, so it is only a fallback.
		if st := statuses[sub.ID]; st == "closed" || (sub.Status == "" && st != "") {
			sub.Status = st
		}
	}
}

// child resolves one spawn: v1 outputs carry the child id; v2 outputs carry
// only the agent path. The daemon answers first, then the state DB, then a
// search of the sessions directory.
func (l *subagentLinker) child(callID, childID, output string) (childThread, bool) {
	key := spawnKey{l.sessionsRoot, l.parent, callID}
	linkMu.Lock()
	c, ok := spawnLinks[key]
	linkMu.Unlock()
	if ok {
		return c, true
	}
	if childID != "" {
		c, ok = l.childByID(childID)
		if !ok { // neither the daemon nor the DB knows it (older Codex)
			c, ok = childThread{id: childID}, true
		}
	} else if _, _, agentPath := spawnResult(output); agentPath != "" {
		if id := l.daemonChildID(callID); id != "" {
			c, ok = l.childByID(id)
		}
		if !ok {
			c, ok = dbChildByPath(l.stateDB(), l.parent, agentPath)
		}
	}
	if !ok {
		return childThread{}, false
	}
	if c.rollout == "" {
		c.rollout = findRolloutPathIn(l.sessionsRoot, c.id)
	}
	if c.rollout != "" {
		linkMu.Lock()
		spawnLinks[key] = c
		rolloutByID[threadKey{l.sessionsRoot, c.id}] = c.rollout
		linkMu.Unlock()
	}
	return c, true
}

// daemonChildID finds the child of a v2 spawn from the subAgentActivity item
// the daemon records for the spawn call (its id is the call id).
func (l *subagentLinker) daemonChildID(callID string) string {
	c, turn := daemonConn(), l.turns[callID]
	if c == nil || turn == "" || !isLiveRoot(l.sessionsRoot) {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), daemonLookupTimeout)
	defer cancel()
	var res struct {
		Data []struct {
			Item struct {
				Type          string `json:"type"`
				ID            string `json:"id"`
				AgentThreadID string `json:"agentThreadId"`
			} `json:"item"`
		} `json:"data"`
	}
	if c.call(ctx, "thread/items/list", map[string]any{"threadId": l.parent, "turnId": turn}, &res) != nil {
		return ""
	}
	for _, d := range res.Data {
		if d.Item.Type == "subAgentActivity" && d.Item.ID == callID {
			return d.Item.AgentThreadID
		}
	}
	return ""
}

func (l *subagentLinker) childByID(id string) (childThread, bool) {
	if !isLiveRoot(l.sessionsRoot) {
		return childThread{}, false
	}
	if c, ok := daemonThread(id); ok {
		return c, true
	}
	return dbChildByID(l.stateDB(), id)
}

// daemonThread reads a thread's rollout path and nickname from the daemon.
func daemonThread(id string) (childThread, bool) {
	c := daemonConn()
	if c == nil {
		return childThread{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), daemonLookupTimeout)
	defer cancel()
	var res struct {
		Thread struct {
			ID            string  `json:"id"`
			Path          *string `json:"path"`
			AgentNickname *string `json:"agentNickname"`
		} `json:"thread"`
	}
	if c.call(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": false}, &res) != nil || res.Thread.ID == "" {
		return childThread{}, false
	}
	return childThread{id: res.Thread.ID, rollout: strVal(res.Thread.Path), nickname: strVal(res.Thread.AgentNickname)}, true
}

// subagentRollout finds a subagent's rollout: cached, then the daemon, then the
// state DB, then a search of sessionsRoot.
func subagentRollout(sessionsRoot, id string) string {
	key := threadKey{sessionsRoot, id}
	linkMu.Lock()
	p := rolloutByID[key]
	linkMu.Unlock()
	if p != "" {
		return p
	}
	l := subagentLinker{sessionsRoot: sessionsRoot}
	defer l.close()
	if c, ok := l.childByID(id); ok {
		p = c.rollout
	}
	if p == "" {
		p = findRolloutPathIn(sessionsRoot, id)
	}
	if p != "" {
		linkMu.Lock()
		rolloutByID[key] = p
		linkMu.Unlock()
	}
	return p
}

// spawnTurns maps each spawn_agent call id to its turn id, which the daemon's
// item lookup needs.
func spawnTurns(lines []rolloutLine) map[string]string {
	out := map[string]string{}
	for _, ln := range lines {
		p := ln.Payload
		if ln.Type == "response_item" && p.Type == "function_call" && p.Name == "spawn_agent" && p.Meta.TurnID != "" {
			out[p.CallID] = p.Meta.TurnID
		}
	}
	return out
}
