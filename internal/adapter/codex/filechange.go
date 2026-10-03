package codex

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// fileUpdateChange is one file of an app-server fileChange item. Diff holds the
// content of an added or deleted file, or the unified diff of an update.
type fileUpdateChange struct {
	Path string `json:"path"`
	Kind struct {
		Type     string  `json:"type"` // add, delete, update
		MovePath *string `json:"move_path"`
	} `json:"kind"`
	Diff string `json:"diff"`
}

type fileChangeItem struct {
	Type    string             `json:"type"`
	ID      string             `json:"id"`
	Changes []fileUpdateChange `json:"changes"`
}

// patchInput writes changes as an apply_patch input, so a file-change approval
// renders like the apply_patch call it approves.
func patchInput(changes []fileUpdateChange) string {
	if len(changes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("*** Begin Patch\n")
	for _, c := range changes {
		switch c.Kind.Type {
		case "add":
			b.WriteString("*** Add File: " + c.Path + "\n")
			for _, l := range strings.Split(strings.TrimSuffix(c.Diff, "\n"), "\n") {
				b.WriteString("+" + l + "\n")
			}
		case "delete":
			b.WriteString("*** Delete File: " + c.Path + "\n")
		default:
			b.WriteString("*** Update File: " + c.Path + "\n")
			if mv := strVal(c.Kind.MovePath); mv != "" {
				b.WriteString("*** Move to: " + mv + "\n")
			}
			b.WriteString(hunks(c.Diff))
		}
	}
	b.WriteString("*** End Patch")
	return b.String()
}

// hunks keeps a unified diff's hunks, dropping file headers and the "Moved to:"
// trailer the app-server appends for a move.
func hunks(diff string) string {
	diff, _, _ = strings.Cut(diff, "\n\nMoved to: ")
	var b strings.Builder
	inHunk := false
	for _, l := range strings.Split(strings.TrimSuffix(diff, "\n"), "\n") {
		if strings.HasPrefix(l, "@@") {
			inHunk = true
		}
		if inHunk {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

func fileChangeKey(threadID, itemID string) string { return threadID + "\x00" + itemID }

// noteFileChange remembers a started fileChange item's patch until the item
// completes, for the approval that follows it.
func (d *discoverer) noteFileChange(method string, params json.RawMessage) {
	var p struct {
		ThreadID string         `json:"threadId"`
		Item     fileChangeItem `json:"item"`
	}
	if json.Unmarshal(params, &p) != nil || p.Item.Type != "fileChange" {
		return
	}
	key := fileChangeKey(p.ThreadID, p.Item.ID)
	d.mu.Lock()
	defer d.mu.Unlock()
	if method == "item/completed" {
		delete(d.fileChanges, key)
		return
	}
	d.fileChanges[key] = patchInput(p.Item.Changes)
}

// fileChangeInput returns the patch a file-change approval is about. The request
// names only the item: like Codex's TUI, this takes it from the item's start, or
// from the turn's history for a request replayed on subscribe.
func (d *discoverer) fileChangeInput(ctx context.Context, c *rpcConn, pr *pendingRequest) string {
	d.mu.Lock()
	in, ok := d.fileChanges[fileChangeKey(pr.threadID, pr.itemID)]
	d.mu.Unlock()
	if ok {
		return in
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var res struct {
		Data []struct {
			Item fileChangeItem `json:"item"`
		} `json:"data"`
	}
	params := map[string]any{"threadId": pr.threadID, "turnId": pr.turnID, "sortDirection": "desc"}
	if c.call(ctx, "thread/items/list", params, &res) != nil {
		return ""
	}
	for _, d := range res.Data {
		if d.Item.Type == "fileChange" && d.Item.ID == pr.itemID {
			return patchInput(d.Item.Changes)
		}
	}
	return ""
}
