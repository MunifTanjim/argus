package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/orch"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
	"github.com/google/uuid"
)

var errOrchDisabled = errors.New("agent sessions are disabled on this node")

// localTarget resolves a bare or own-prefixed composite ID to a session on this node.
func (d *Node) localTarget(id string) (session.Session, error) {
	node, rest, prefixed := session.SplitCompositeID(id)
	if prefixed && node == d.id {
		if s, found := d.reg.Get(rest); found {
			return s, nil
		}
	}
	if s, found := d.reg.Get(id); found {
		return s, nil
	}
	if prefixed && node != d.id && strings.Contains(rest, ":") {
		return session.Session{}, fmt.Errorf("session %s is on node %s; cross-machine calls are not supported yet", id, node)
	}
	return session.Session{}, fmt.Errorf("unknown session: %s", id)
}

// composite returns s as orch.* clients see it, with session IDs prefixed by the node ID.
func (d *Node) composite(s session.Session) session.Session {
	s = d.withCaps(s)
	s.ID = d.compositeID(s)
	s.Parent = s.Parent.Composite(d.id)
	return s
}

func (d *Node) compositeID(s session.Session) string {
	return session.CompositeID(d.id, s.ID)
}

// lastAssistantText joins the text entries that follow the last user entry.
func lastAssistantText(entries []transcript.Entry) string {
	start := 0
	for i, e := range entries {
		if e.Kind == transcript.EntryUser {
			start = i + 1
		}
	}
	var parts []string
	for _, e := range entries[start:] {
		if e.Kind == transcript.EntryText {
			parts = append(parts, e.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (d *Node) transcriptView(s session.Session) (transcript.TranscriptView, error) {
	if s.TranscriptPath == "" {
		return transcript.TranscriptView{}, nil
	}
	return d.adapterFor(s.Agent).ReadTranscriptView(s.TranscriptPath)
}

func (d *Node) handleOrchSessionCurrent(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.CallerParams](params)
	if err != nil {
		return nil, err
	}
	caller, err := d.callerFrom(ctx, p.Caller)
	if err != nil {
		return nil, err
	}
	return d.currentSession(caller)
}

func (d *Node) currentSession(caller callerRef) (session.Session, error) {
	return d.composite(*caller.Local), nil
}

func (d *Node) handleOrchSessionList(ctx context.Context, params json.RawMessage) (any, error) {
	if _, err := api.Decode[api.CallerParams](params); err != nil {
		return nil, err
	}
	return api.OrchListResult{Sessions: d.list()}, nil
}

func (d *Node) list() []session.Session {
	snap := d.reg.Snapshot()
	for i := range snap {
		snap[i] = d.composite(snap[i])
	}
	return snap
}

// orchCall decodes an orch.* request that needs the store, resolving the
// calling session when caller is non-nil.
func orchCall[P any](ctx context.Context, d *Node, params json.RawMessage, caller func(P) api.CallerInfo) (P, callerRef, error) {
	p, err := api.Decode[P](params)
	if err != nil {
		return p, callerRef{}, err
	}
	if d.orch == nil {
		return p, callerRef{}, errOrchDisabled
	}
	if caller == nil {
		return p, callerRef{}, nil
	}
	c, err := d.callerFrom(ctx, caller(p))
	return p, c, err
}

func (d *Node) handleOrchSessionGet(ctx context.Context, params json.RawMessage) (any, error) {
	p, _, err := orchCall[api.OrchTargetParams](ctx, d, params, nil)
	if err != nil {
		return nil, err
	}
	return d.getSession(ctx, p.SessionID)
}

func (d *Node) getSession(ctx context.Context, id string) (api.OrchGetResult, error) {
	s, err := d.localTarget(id)
	if err != nil {
		return api.OrchGetResult{}, err
	}
	queued := []api.Message{}
	if key := actorKey(s); key != "" {
		if queued, err = d.orch.Queued(ctx, d.actor(s)); err != nil {
			return api.OrchGetResult{}, err
		}
	}
	return api.OrchGetResult{Session: d.composite(s), Queued: queued}, nil
}

func (d *Node) handleOrchSessionOutput(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.OrchTargetParams](params)
	if err != nil {
		return nil, err
	}
	return d.output(p.SessionID)
}

func (d *Node) output(id string) (api.OrchOutputResult, error) {
	s, err := d.localTarget(id)
	if err != nil {
		return api.OrchOutputResult{}, err
	}
	view, err := d.transcriptView(s)
	if err != nil {
		return api.OrchOutputResult{}, err
	}
	return api.OrchOutputResult{Text: lastAssistantText(view.Entries)}, nil
}

func (d *Node) handleOrchSessionTranscript(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.OrchTranscriptParams](params)
	if err != nil {
		return nil, err
	}
	return d.transcript(p.SessionID, p.Last)
}

func (d *Node) transcript(id string, last int) (api.OrchTranscriptResult, error) {
	s, err := d.localTarget(id)
	if err != nil {
		return api.OrchTranscriptResult{}, err
	}
	view, err := d.transcriptView(s)
	if err != nil {
		return api.OrchTranscriptResult{}, err
	}
	entries := view.Entries
	if last > 0 && last < len(entries) {
		entries = entries[len(entries)-last:]
	}
	return api.OrchTranscriptResult{Entries: entries}, nil
}

func (d *Node) handleOrchSessionSpawn(ctx context.Context, params json.RawMessage) (any, error) {
	p, caller, err := orchCall(ctx, d, params, func(p api.OrchSpawnParams) api.CallerInfo { return p.Caller })
	if err != nil {
		return nil, err
	}
	return d.spawnFor(ctx, caller, p)
}

func (d *Node) spawnFor(ctx context.Context, caller callerRef, p api.OrchSpawnParams) (api.OrchSpawnResult, error) {
	if !filepath.IsAbs(p.Cwd) {
		return api.OrchSpawnResult{}, errors.New("cwd must be an absolute directory path")
	}
	if !d.orchestrated(p.Agent) {
		return api.OrchSpawnResult{}, fmt.Errorf("%s does not support agent sessions", p.Agent)
	}
	parent := caller.Actor
	if parent.Key == "" {
		return api.OrchSpawnResult{}, notReady(caller.SessionID)
	}
	var req orch.Request
	if p.RequestID != "" {
		req = newRequest(p.RequestID, p.Agent, p.Cwd, p.Name, p.Model, p.Prompt)
		defer d.orchRequests.lock(parent, req.ID)()
		if res, ok, err := d.spawnedRequest(ctx, parent, req); err != nil || ok {
			return res, err
		}
	}

	var task api.Message
	prompt := p.Prompt
	if p.Prompt != "" {
		task = api.Message{
			ID: orch.NewID(d.id), Kind: api.MessageTask, From: parent, Body: p.Prompt, State: api.MessageDelivered,
		}
		prompt = orch.RenderPrompt([]orch.Delivery{{Msg: task, From: caller.Label, FromSession: caller.SessionID}})
	}

	a := d.adapterFor(p.Agent)
	var presetID string
	if ps, ok := a.(adapter.SessionIDPresetter); ok && ps.PresetsSessionID() {
		presetID = uuid.NewString()
	}
	_, headless := a.(adapter.Spawner)
	if !headless && presetID == "" {
		return api.OrchSpawnResult{}, fmt.Errorf("%s cannot be linked to its parent at spawn", p.Agent)
	}
	spawned, err := d.spawnFn(ctx, api.SpawnParams{
		Agent: p.Agent, Cwd: p.Cwd, Name: p.Name, Model: p.Model, Prompt: prompt,
	}, presetID)
	if err != nil {
		return api.OrchSpawnResult{}, err
	}
	childKey := spawned.SessionID
	if !headless {
		childKey = registry.AgentSessionKey(a.Agent(), presetID)
	}

	child := api.Actor{Node: d.id, Key: childKey}
	task.To = child
	link := api.Link{Child: child, Parent: parent, TaskID: task.ID, ChildSessionID: session.CompositeID(d.id, spawned.SessionID)}
	if err := d.orch.LinkSpawned(ctx, link, task, req); err != nil {
		d.abortSpawn(ctx, p.Agent, childKey, spawned)
		return api.OrchSpawnResult{}, err
	}
	d.orchChanged()
	return api.OrchSpawnResult{SessionID: link.ChildSessionID, TaskID: task.ID}, nil
}

// abortSpawn ends a child that could not be linked, so it never runs unlinked
// with a task id that does not exist.
func (d *Node) abortSpawn(ctx context.Context, agent, childKey string, spawned api.SpawnResult) {
	s, ok := d.reg.Get(spawned.SessionID)
	if !ok {
		_, agentSessionID, _ := strings.Cut(childKey, ":")
		s = session.Session{
			ID: spawned.SessionID, Agent: agent, AgentSessionID: agentSessionID,
			Tmux: session.TmuxLocation{Server: session.TmuxServerArgus, PaneID: spawned.PaneID},
		}
	}
	if err := d.killSession(context.WithoutCancel(ctx), s); err != nil {
		d.log.Warn("orch: kill unlinked child failed", "session", spawned.SessionID, "err", err)
	}
}

// orchestrated reports whether agent sessions can take part in messages and links.
func (d *Node) orchestrated(agent string) bool {
	_, ok := d.adapters[agent].(adapter.CallerIdentity)
	return ok
}

func (d *Node) handleOrchMessageSend(ctx context.Context, params json.RawMessage) (any, error) {
	p, caller, err := orchCall(ctx, d, params, func(p api.OrchSendParams) api.CallerInfo { return p.Caller })
	if err != nil {
		return nil, err
	}
	return d.send(ctx, caller, p.SessionID, p.Text, p.RequestID)
}

func (d *Node) send(ctx context.Context, caller callerRef, targetID, text, requestID string) (api.OrchSendResult, error) {
	target, err := d.localTarget(targetID)
	if err != nil {
		return api.OrchSendResult{}, err
	}
	return d.sendText(ctx, caller, target, targetID, text, requestID)
}

// sendText queues text from the caller to target.
func (d *Node) sendText(ctx context.Context, caller callerRef, target session.Session, targetID, text, requestID string) (api.OrchSendResult, error) {
	from := caller.Actor
	if !d.orchestrated(target.Agent) {
		return api.OrchSendResult{}, fmt.Errorf("%s does not support agent messages", targetID)
	}
	to := d.actor(target)
	if to.Key == "" {
		return api.OrchSendResult{}, notReady(targetID)
	}
	if from.Key == "" {
		return api.OrchSendResult{}, notReady(caller.SessionID)
	}
	var req orch.Request
	if requestID != "" {
		req = newRequest(requestID, to.Node, to.Key, text)
		defer d.orchRequests.lock(from, req.ID)()
		if res, ok, err := d.sentRequest(ctx, from, req); err != nil || ok {
			return res, err
		}
	}
	m, err := d.orch.InsertRequest(ctx, api.Message{
		Kind: api.MessageText, From: from, To: to, Body: text, State: api.MessageQueued,
	}, req)
	if err != nil {
		return api.OrchSendResult{}, err
	}
	d.orchKick()
	d.orchChanged()
	return api.OrchSendResult{MessageID: m.ID}, nil
}

func (d *Node) handleOrchMessageReply(ctx context.Context, params json.RawMessage) (any, error) {
	p, caller, err := orchCall(ctx, d, params, func(p api.OrchReplyParams) api.CallerInfo { return p.Caller })
	if err != nil {
		return nil, err
	}
	return d.reply(ctx, caller, p.MessageID, p.Text)
}

// message finds a message by ID. A bare ID without a node prefix also matches
// the message of this node with that ID.
func (d *Node) message(ctx context.Context, id string) (api.Message, bool, error) {
	m, ok, err := d.orch.Get(ctx, id)
	if err != nil || ok {
		return m, ok, err
	}
	if !strings.Contains(id, ":") {
		return d.orch.Get(ctx, session.CompositeID(d.id, id))
	}
	return api.Message{}, false, nil
}

func (d *Node) mustMessage(ctx context.Context, id string) (api.Message, error) {
	m, ok, err := d.message(ctx, id)
	if err != nil {
		return api.Message{}, err
	}
	if !ok {
		return api.Message{}, fmt.Errorf("unknown message: %s", id)
	}
	return m, nil
}

func (d *Node) reply(ctx context.Context, caller callerRef, messageID, text string) (api.OrchSendResult, error) {
	orig, err := d.mustMessage(ctx, messageID)
	if err != nil {
		return api.OrchSendResult{}, err
	}
	if caller.Actor.Key == "" || caller.Actor != orig.To {
		return api.OrchSendResult{}, fmt.Errorf("only the recipient of %s can reply to it", messageID)
	}
	owner := api.Actor{Node: d.id, Key: api.ActorOwner}
	if orig.From.Key == api.ActorArgus {
		return api.OrchSendResult{}, errors.New("argus messages cannot be replied to: answer an interaction with argus session respond")
	}
	state := api.MessageQueued
	if orig.From == owner {
		state = api.MessageDelivered
	}
	m, err := d.insertReply(ctx, caller, orig, text, state)
	if err != nil {
		return api.OrchSendResult{}, err
	}
	if orig.From == owner {
		d.notifyOwner(ctx, m, caller.Label)
	}
	return api.OrchSendResult{MessageID: m.ID}, nil
}

// insertReply stores the caller's reply to orig in state and marks orig
// replied.
func (d *Node) insertReply(ctx context.Context, caller callerRef, orig api.Message, text string, state api.MessageState) (api.Message, error) {
	if to := orig.From; !reservedKey(to.Key) {
		if _, live := d.sessionForKey(to.Key); !live {
			return api.Message{}, fmt.Errorf("the sender of %s is not running; the reply cannot be delivered", orig.ID)
		}
	}
	m, open, err := d.orch.ReplyTo(ctx, orig.ID, api.Message{
		Kind: api.MessageReply, From: caller.Actor, To: orig.From, Body: text, ReplyTo: orig.ID, State: state,
	})
	if err != nil {
		return api.Message{}, err
	}
	if !open {
		return api.Message{}, fmt.Errorf("%s is no longer open; the reply cannot be sent", orig.ID)
	}
	d.orchKick()
	d.orchChanged()
	return m, nil
}

func (d *Node) handleOrchInteractionRespond(ctx context.Context, params json.RawMessage) (any, error) {
	p, caller, err := orchCall(ctx, d, params, func(p api.OrchRespondParams) api.CallerInfo { return p.Caller })
	if err != nil {
		return nil, err
	}
	return nil, d.respond(ctx, caller, p)
}

// respond claims the notice before answering, so a parent's answer is never
// reported back as answered outside argus.
func (d *Node) respond(ctx context.Context, caller callerRef, p api.OrchRespondParams) error {
	by := caller.Actor
	n, err := d.mustMessage(ctx, p.MessageID)
	if err != nil {
		return err
	}
	answered := fmt.Errorf("%s is already answered", n.ID)
	if n.Kind != api.MessageInteraction || (n.State != api.MessageQueued && n.State != api.MessageDelivered) {
		return answered
	}
	if !n.Interaction.ParentCanAnswer() {
		who := "the child"
		if n.Child != nil {
			who = n.Child.Key
			if c, live := d.sessionForKey(n.Child.Key); live {
				who = fmt.Sprintf("%s (%s)", sessionLabel(c), d.compositeID(c))
			}
		}
		return fmt.Errorf("%s waits for the user; only the user can answer it, in the child's session", who)
	}
	p.Answers = splitAnswers(p.Answers, n.Interaction)
	if err := checkAnswer(p, n.Interaction); err != nil {
		return err
	}
	childKey := ""
	if n.Child != nil {
		childKey = n.Child.Key
	}
	child, live := d.sessionForKey(childKey)
	if !live {
		return answered
	}
	link, ok, err := d.orch.Link(ctx, api.Actor{Node: d.id, Key: childKey})
	if err != nil {
		return err
	}
	if !ok || caller.Actor.Key == "" || link.Parent != caller.Actor {
		return fmt.Errorf("only the parent of %s can answer its prompts", d.compositeID(child))
	}
	if pendingSig(child) == "" || pendingSig(child) != interactionSig(n.Interaction) {
		return answered
	}
	if !d.canRespond(child) {
		return fmt.Errorf("%s cannot be answered through argus", n.ID)
	}
	prev, claimed, err := d.orch.ClaimInteraction(ctx, n.ID, by)
	if err != nil {
		return err
	}
	if !claimed {
		return answered
	}
	params, err := json.Marshal(api.RespondParams{
		SessionID: child.ID, Behavior: p.Behavior, Reason: p.Reason,
		Answers: p.Answers, RequestID: child.Interaction.RequestID,
	})
	if err == nil {
		_, err = d.handleSessionRespond(ctx, params)
	}
	if err != nil {
		if _, rerr := d.orch.ReleaseInteraction(ctx, n.ID, by, prev); rerr != nil {
			d.log.Warn("orch: release interaction failed", "id", n.ID, "err", rerr)
		}
		return err
	}
	d.orchChanged()
	return nil
}

// actorID names an actor across nodes, as <node>:<key>.
func actorID(p api.Actor) string {
	if p.Key == "" {
		return ""
	}
	return p.Node + ":" + p.Key
}

// splitAnswers re-splits each "<question>=<label>" pair at the end of a question
// of the notice, because the question or the label can contain "=".
func splitAnswers(answers map[string]any, i *session.Interaction) map[string]any {
	if len(answers) == 0 || i == nil {
		return answers
	}
	isQuestion := func(q string) bool {
		return slices.ContainsFunc(i.Questions, func(s session.QuestionSpec) bool { return s.Question == q })
	}
	out := make(map[string]any, len(answers))
	for k, v := range answers {
		label, ok := v.(string)
		if isQuestion(k) || !ok {
			out[k] = v
			continue
		}
		pair, q := k+"="+label, ""
		for _, s := range i.Questions {
			if len(s.Question) > len(q) && strings.HasPrefix(pair, s.Question+"=") {
				q = s.Question
			}
		}
		if q == "" {
			out[k] = v
			continue
		}
		out[q] = pair[len(q)+1:]
	}
	return out
}

// checkAnswer rejects an answer that does not fit the notice: a missing answer
// must never approve a prompt, and a mode change needs an option the notice offers.
func checkAnswer(p api.OrchRespondParams, i *session.Interaction) error {
	if i == nil {
		i = &session.Interaction{}
	}
	forms := 0
	for _, given := range []bool{p.Behavior != "", len(p.Answers) > 0} {
		if given {
			forms++
		}
	}
	if forms != 1 {
		return errors.New("give exactly one of deny or answers")
	}
	switch {
	case p.Behavior == "allow":
		return errors.New("answer a question with answers, or decline it with deny")
	case p.Behavior != "":
		if p.Behavior != "deny" {
			return fmt.Errorf("behavior must be deny, got %q", p.Behavior)
		}
	default:
		if i.Kind != session.InteractionQuestion {
			return errors.New("answers apply only to a question interaction")
		}
		for q := range p.Answers {
			if !slices.ContainsFunc(i.Questions, func(s session.QuestionSpec) bool { return s.Question == q }) {
				return fmt.Errorf("%q is not a question of this interaction", q)
			}
		}
	}
	return nil
}

func (d *Node) handleOrchSessionRename(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.OrchRenameParams](params)
	if err != nil {
		return nil, err
	}
	return d.rename(ctx, p.SessionID, p.Name)
}

// rename names any session the node holds: unlike kill, it needs no caller.
func (d *Node) rename(ctx context.Context, id, name string) (api.SessionRenameResult, error) {
	s, err := d.localTarget(id)
	if err != nil {
		return api.SessionRenameResult{}, err
	}
	return d.renameSession(ctx, s, name)
}

func (d *Node) handleOrchSessionKill(ctx context.Context, params json.RawMessage) (any, error) {
	p, caller, err := orchCall(ctx, d, params, func(p api.OrchTargetParams) api.CallerInfo { return p.Caller })
	if err != nil {
		return nil, err
	}
	return d.kill(ctx, caller, p.SessionID)
}

func (d *Node) kill(ctx context.Context, caller callerRef, targetID string) (api.OrchKillResult, error) {
	target, err := d.localTarget(targetID)
	if err != nil {
		return api.OrchKillResult{}, err
	}
	by := "self"
	if caller.Actor.Key == "" || caller.Actor != d.actor(target) {
		link, ok, err := d.orch.Link(ctx, d.actor(target))
		if err != nil {
			return api.OrchKillResult{}, err
		}
		if !ok || caller.Actor.Key == "" || link.Parent != caller.Actor {
			return api.OrchKillResult{}, fmt.Errorf("only the parent of %s or the session itself can kill it", d.compositeID(target))
		}
		by = "parent"
	}
	killed, err := d.killTree(ctx, target, by)
	if err != nil {
		if len(killed) > 0 {
			err = fmt.Errorf("%w (killed: %s)", err, strings.Join(killed, ", "))
		}
		return api.OrchKillResult{}, err
	}
	return api.OrchKillResult{Killed: killed}, nil
}
