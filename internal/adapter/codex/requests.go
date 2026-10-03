package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// handleRequest queues a thread's server request and shows it on the root
// session. Requests for untracked threads get no reply, so the TUI that shares
// the thread answers them.
func (d *discoverer) handleRequest(ctx context.Context, c *rpcConn, m inbound) {
	pr, ok := parseRequest(m)
	if !ok || pr.threadID == "" {
		return
	}
	pr.conn = c
	if pr.itemID != "" {
		pr.interaction.ToolInput = d.fileChangeInput(ctx, c, pr)
	}
	d.mu.Lock()
	root := d.rootOfLocked(pr.threadID)
	if _, tracked := d.threads[root]; !tracked {
		d.mu.Unlock()
		return
	}
	for _, q := range d.pending[root] {
		// A re-subscribe replays open requests.
		if q.threadID == pr.threadID && bytes.Equal(q.id, pr.id) {
			d.mu.Unlock()
			return
		}
	}
	d.reqSeq++
	pr.interaction.RequestID = strconv.FormatUint(d.reqSeq, 10)
	d.pending[root] = append(d.pending[root], pr)
	d.mu.Unlock()
	d.refreshCard(root)
}

// dropPendingFrom removes requests asked by threadID once that thread stops
// waiting, which covers an answer given in the TUI.
func (d *discoverer) dropPendingFrom(threadID string) {
	d.mu.Lock()
	root := d.rootOfLocked(threadID)
	changed := d.dropPendingLocked(root, func(pr *pendingRequest) bool { return pr.threadID == threadID })
	d.mu.Unlock()
	if changed && root != threadID {
		d.refreshCard(root)
	}
}

func (d *discoverer) onResolved(params json.RawMessage) {
	var p struct {
		ThreadID  string          `json:"threadId"`
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(params, &p) != nil || p.ThreadID == "" {
		return
	}
	d.mu.Lock()
	root := d.rootOfLocked(p.ThreadID)
	changed := d.dropPendingLocked(root, func(pr *pendingRequest) bool {
		return pr.threadID == p.ThreadID && bytes.Equal(pr.id, p.RequestID)
	})
	d.mu.Unlock()
	if changed {
		d.refreshCard(root)
	}
}

// dropPendingLocked removes root's requests matching drop and reports whether any
// were removed. Caller holds d.mu.
func (d *discoverer) dropPendingLocked(root string, drop func(*pendingRequest) bool) bool {
	q := d.pending[root]
	kept := slices.DeleteFunc(q, drop)
	if len(kept) == 0 {
		delete(d.pending, root)
	} else {
		d.pending[root] = kept
	}
	return len(kept) != len(q)
}

// headPending returns the oldest unanswered request for root. Caller holds d.mu.
func (d *discoverer) headPending(root string) *pendingRequest {
	for _, pr := range d.pending[root] {
		if !pr.answered {
			return pr
		}
	}
	return nil
}

// respond answers the request p names, then shows the next one (or the status
// fallback). The node skips ClearInteraction for codex (see OwnsInteraction), so
// this refresh is the only card update.
func (d *discoverer) respond(ctx context.Context, sess session.Session, p api.RespondParams) error {
	root := sess.AgentSessionID
	// Mark answered before replying so a concurrent respond takes the next request.
	d.mu.Lock()
	head, err := d.claimLocked(root, p)
	d.mu.Unlock()
	if err != nil {
		return err
	}
	if c := d.conn(); head.conn == nil || head.conn != c {
		// Request ids are per connection; this one died with its connection.
		err = fmt.Errorf("codex: request expired after reconnect")
	} else if head.method == "item/tool/requestUserInput" && p.QuestionAction == "cancel" {
		err = d.interruptQuestion(ctx, c, root, head)
	} else {
		err = c.reply(ctx, head.id, replyFor(head, p))
	}
	if err != nil {
		d.mu.Lock()
		head.answered = false
		d.mu.Unlock()
		return err
	}
	d.refreshCard(root)
	return nil
}

// claimLocked picks the request p answers and marks it answered. A respond that
// names its request answers exactly that one; one that does not (older clients)
// answers the oldest. Caller holds d.mu.
func (d *discoverer) claimLocked(root string, p api.RespondParams) (*pendingRequest, error) {
	var pr *pendingRequest
	if p.RequestID != "" {
		for _, q := range d.pending[root] {
			if !q.answered && q.interaction.RequestID == p.RequestID {
				pr = q
				break
			}
		}
		if pr == nil {
			return nil, errors.New("codex: the request was already answered or withdrawn")
		}
	} else if pr = d.headPending(root); pr == nil {
		return nil, fmt.Errorf("codex: no pending request for session %s", root)
	}
	if err := checkRespond(pr, p); err != nil {
		return nil, err
	}
	pr.answered = true
	return pr, nil
}

// checkRespond rejects a respond that does not fit its request: answers and
// question actions go to questions, and decisions to approvals, as one of the
// offered options.
func checkRespond(pr *pendingRequest, p api.RespondParams) error {
	kind := pr.interaction.Kind
	if p.Kind != "" && p.Kind != string(kind) {
		return fmt.Errorf("codex: the answer is for a %s, but the session is showing a %s", p.Kind, kind)
	}
	if kind == session.InteractionQuestion {
		switch p.QuestionAction {
		case "":
		case "cancel":
		case "chat":
			return errors.New("codex: questions cannot be answered with chat; submit or cancel")
		default:
			return fmt.Errorf("codex: unknown question action %q", p.QuestionAction)
		}
		if p.OptionValue != "" {
			return errors.New("codex: the session is showing a question, not an approval")
		}
		return nil
	}
	if len(p.Answers) > 0 || p.QuestionAction != "" {
		return errors.New("codex: the session is showing an approval, not a question")
	}
	if p.OptionValue != "" {
		if !slices.ContainsFunc(pr.interaction.Options, func(o session.DecisionOption) bool { return o.Value == p.OptionValue }) {
			return fmt.Errorf("codex: %q is not an option for this request", p.OptionValue)
		}
		return nil
	}
	if p.Behavior != "allow" && p.Behavior != "deny" {
		return errors.New("codex: the answer has no decision")
	}
	return nil
}

// interruptQuestion handles Cancel on a question as Codex's TUI handles Esc: it
// interrupts the asking thread's turn, which ends every request that turn holds.
func (d *discoverer) interruptQuestion(ctx context.Context, c *rpcConn, root string, pr *pendingRequest) error {
	turn, running, err := latestTurn(ctx, c, pr.threadID)
	if err != nil {
		return err
	}
	if running {
		if err := c.call(ctx, "turn/interrupt", map[string]any{"threadId": pr.threadID, "turnId": turn}, nil); err != nil {
			return err
		}
	}
	d.mu.Lock()
	d.dropPendingLocked(root, func(q *pendingRequest) bool { return q.threadID == pr.threadID })
	d.mu.Unlock()
	return nil
}

// latestTurn returns the id of a thread's most recent turn and whether it is
// still running.
func latestTurn(ctx context.Context, c *rpcConn, threadID string) (id string, running bool, err error) {
	var res struct {
		Data []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	params := map[string]any{"threadId": threadID, "limit": 1, "sortDirection": "desc", "itemsView": "notLoaded"}
	if err := c.call(ctx, "thread/turns/list", params, &res); err != nil {
		return "", false, err
	}
	if len(res.Data) == 0 {
		return "", false, nil
	}
	return res.Data[0].ID, res.Data[0].Status == "inProgress", nil
}
