package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/mesh"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

var errMeshDisabled = errors.New("agent sessions are disabled on this node")

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

// composite returns s as agentctl clients see it, with session IDs prefixed by the node ID.
func (d *Node) composite(s session.Session) session.Session {
	s = d.withCaps(s)
	s.ID = d.compositeID(s)
	if s.Parent != nil && s.Parent.SessionID != "" {
		p := *s.Parent
		p.SessionID = session.CompositeID(d.id, p.SessionID)
		s.Parent = &p
	}
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

func (d *Node) handleAgentWhoami(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.CallerParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.resolveCaller(ctx, p.Caller)
	if err != nil {
		return nil, err
	}
	return d.composite(s), nil
}

func (d *Node) handleAgentList(context.Context, json.RawMessage) (any, error) {
	snap := d.reg.Snapshot()
	for i := range snap {
		snap[i] = d.composite(snap[i])
	}
	return snap, nil
}

// meshCall decodes an agentctl request that needs the mesh, resolving the
// calling session when caller is non-nil.
func meshCall[P any](ctx context.Context, d *Node, params json.RawMessage, caller func(P) api.CallerInfo) (P, session.Session, error) {
	p, err := api.Decode[P](params)
	if err != nil {
		return p, session.Session{}, err
	}
	if d.mesh == nil {
		return p, session.Session{}, errMeshDisabled
	}
	if caller == nil {
		return p, session.Session{}, nil
	}
	s, err := d.resolveCaller(ctx, caller(p))
	return p, s, err
}

func (d *Node) handleAgentShow(ctx context.Context, params json.RawMessage) (any, error) {
	p, _, err := meshCall[api.AgentTargetParams](ctx, d, params, nil)
	if err != nil {
		return nil, err
	}
	s, err := d.localTarget(p.SessionID)
	if err != nil {
		return nil, err
	}
	queued, err := d.mesh.Queued(ctx, partyKey(s))
	if err != nil {
		return nil, err
	}
	return api.AgentShowResult{Session: d.composite(s), Queued: queued}, nil
}

func (d *Node) handleAgentOutput(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.AgentTargetParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.localTarget(p.SessionID)
	if err != nil {
		return nil, err
	}
	view, err := d.transcriptView(s)
	if err != nil {
		return nil, err
	}
	return api.AgentOutputResult{Text: lastAssistantText(view.Entries)}, nil
}

func (d *Node) handleAgentTranscript(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.AgentTranscriptParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.localTarget(p.SessionID)
	if err != nil {
		return nil, err
	}
	view, err := d.transcriptView(s)
	if err != nil {
		return nil, err
	}
	entries := view.Entries
	if p.Last > 0 && p.Last < len(entries) {
		entries = entries[len(entries)-p.Last:]
	}
	return api.AgentTranscriptResult{Entries: entries}, nil
}

func (d *Node) handleAgentSpawn(ctx context.Context, params json.RawMessage) (any, error) {
	p, caller, err := meshCall(ctx, d, params, func(p api.AgentSpawnParams) api.CallerInfo { return p.Caller })
	if err != nil {
		return nil, err
	}
	if !d.orchestrated(p.Agent) {
		return nil, fmt.Errorf("%s does not support agent sessions", p.Agent)
	}
	parent := d.party(caller)

	var task api.Message
	prompt := p.Prompt
	if p.Prompt != "" {
		task = api.Message{
			ID: mesh.NewID(), Kind: api.MessageTask, From: parent, Body: p.Prompt,
			State: api.MessageDelivered,
		}
		prompt = mesh.Render(task, sessionLabel(caller), d.compositeID(caller))
	}

	spawnParams, err := json.Marshal(api.SpawnParams{
		Agent: p.Agent, Cwd: p.Cwd, Name: p.Name, Model: p.Model, Prompt: prompt,
	})
	if err != nil {
		return nil, err
	}
	out, err := d.handleSessionSpawn(ctx, spawnParams)
	if err != nil {
		return nil, err
	}
	spawned, ok := out.(api.SpawnResult)
	if !ok {
		return nil, fmt.Errorf("unexpected spawn result %T", out)
	}

	child := api.Party{Node: d.id, Key: spawned.SessionID}
	if err := d.mesh.PutLink(ctx, api.Link{Child: child, Parent: parent, TaskID: task.ID}); err != nil {
		return nil, err
	}
	if task.ID != "" {
		task.To = child
		if _, err := d.mesh.Insert(ctx, task); err != nil {
			return nil, err
		}
	}
	if s, live := d.reg.Get(spawned.SessionID); live && partyKey(s) != child.Key {
		if err := d.mesh.Rekey(ctx, child.Key, partyKey(s)); err != nil {
			return nil, err
		}
	}
	d.meshChanged()
	return api.AgentSpawnResult{SessionID: session.CompositeID(d.id, spawned.SessionID), TaskID: task.ID}, nil
}

// orchestrated reports whether agent sessions can take part in messages and links.
func (d *Node) orchestrated(agent string) bool {
	_, ok := d.adapters[agent].(adapter.CallerIdentity)
	return ok
}

func (d *Node) handleAgentSend(ctx context.Context, params json.RawMessage) (any, error) {
	p, caller, err := meshCall(ctx, d, params, func(p api.AgentSendParams) api.CallerInfo { return p.Caller })
	if err != nil {
		return nil, err
	}
	return d.send(ctx, caller, p.SessionID, p.Text)
}

func (d *Node) send(ctx context.Context, caller session.Session, targetID, text string) (api.AgentSendResult, error) {
	target, err := d.localTarget(targetID)
	if err != nil {
		return api.AgentSendResult{}, err
	}
	return d.sendText(ctx, d.party(caller), target, targetID, text)
}

// sendText queues text to target.
func (d *Node) sendText(ctx context.Context, from api.Party, target session.Session, targetID, text string) (api.AgentSendResult, error) {
	if !d.orchestrated(target.Agent) {
		return api.AgentSendResult{}, fmt.Errorf("%s does not support agent messages", targetID)
	}
	m, err := d.mesh.Insert(ctx, api.Message{
		Kind: api.MessageText, From: from, To: d.party(target),
		Body: text, State: api.MessageQueued,
	})
	if err != nil {
		return api.AgentSendResult{}, err
	}
	d.meshKick()
	d.meshChanged()
	return api.AgentSendResult{MessageID: m.ID}, nil
}

func (d *Node) handleAgentReply(ctx context.Context, params json.RawMessage) (any, error) {
	p, caller, err := meshCall(ctx, d, params, func(p api.AgentReplyParams) api.CallerInfo { return p.Caller })
	if err != nil {
		return nil, err
	}
	return d.reply(ctx, caller, p.MessageID, p.Text)
}

func (d *Node) message(ctx context.Context, id string) (api.Message, error) {
	m, ok, err := d.mesh.Get(ctx, id)
	if err != nil {
		return api.Message{}, err
	}
	if !ok {
		return api.Message{}, fmt.Errorf("unknown message: %s", id)
	}
	return m, nil
}

func (d *Node) reply(ctx context.Context, caller session.Session, messageID, text string) (api.AgentSendResult, error) {
	orig, err := d.message(ctx, messageID)
	if err != nil {
		return api.AgentSendResult{}, err
	}
	from := d.party(caller)
	if from.Key != orig.To.Key {
		return api.AgentSendResult{}, fmt.Errorf("only the recipient of %s can reply to it", messageID)
	}
	if orig.From.Key == api.PartyArgus {
		return api.AgentSendResult{}, errors.New("use argus session respond to answer argus notices")
	}
	state := api.MessageQueued
	if orig.From.Key == api.PartyOwner {
		state = api.MessageDelivered
	}
	m, err := d.insertReply(ctx, from, orig, text, state)
	if err != nil {
		return api.AgentSendResult{}, err
	}
	if orig.From.Key == api.PartyOwner {
		d.notifyOwner(ctx, m, sessionLabel(caller))
	}
	return api.AgentSendResult{MessageID: m.ID}, nil
}

// insertReply stores from's reply to orig in state and marks orig replied.
func (d *Node) insertReply(ctx context.Context, from api.Party, orig api.Message, text string, state api.MessageState) (api.Message, error) {
	if k := orig.From.Key; k != api.PartyOwner && k != api.PartyArgus {
		if _, live := d.sessionForKey(k); !live {
			return api.Message{}, fmt.Errorf("the sender of %s is not running; the reply cannot be delivered", orig.ID)
		}
	}
	m, err := d.mesh.Insert(ctx, api.Message{
		Kind: api.MessageReply, From: from, To: orig.From, Body: text,
		ReplyTo: orig.ID, State: state,
	})
	if err != nil {
		return api.Message{}, err
	}
	if err := d.mesh.SetState(ctx, orig.ID, api.MessageReplied); err != nil {
		return api.Message{}, err
	}
	d.meshKick()
	d.meshChanged()
	return m, nil
}

func (d *Node) handleAgentRespond(ctx context.Context, params json.RawMessage) (any, error) {
	p, caller, err := meshCall(ctx, d, params, func(p api.AgentRespondParams) api.CallerInfo { return p.Caller })
	if err != nil {
		return nil, err
	}
	return nil, d.respond(ctx, caller, p)
}

// respond claims the notice before answering, so a parent's answer is never
// reported back as answered outside argus.
func (d *Node) respond(ctx context.Context, caller session.Session, p api.AgentRespondParams) error {
	n, err := d.message(ctx, p.NoticeID)
	if err != nil {
		return err
	}
	answered := fmt.Errorf("%s is already answered", n.ID)
	if n.Kind != api.MessagePromptNotice || (n.State != api.MessageQueued && n.State != api.MessageDelivered) {
		return answered
	}
	if err := checkAnswer(p, n.Interaction); err != nil {
		return err
	}
	child, live := d.sessionForKey(n.Subject)
	if !live {
		return answered
	}
	by := d.party(caller).Key
	link, ok, err := d.mesh.Link(ctx, n.Subject)
	if err != nil {
		return err
	}
	if !ok || link.Parent.Key != by {
		return fmt.Errorf("only the parent of %s can answer its prompts", d.compositeID(child))
	}
	if pendingSig(child) == "" || pendingSig(child) != interactionSig(n.Interaction) {
		return answered
	}
	if !d.canRespond(child) {
		return fmt.Errorf("%s cannot be answered through argus", n.ID)
	}
	prev, claimed, err := d.mesh.ClaimNotice(ctx, n.ID, by)
	if err != nil {
		return err
	}
	if !claimed {
		return answered
	}
	params, err := json.Marshal(api.RespondParams{
		SessionID: child.ID, Behavior: p.Behavior, Reason: p.Reason, OptionValue: p.OptionValue,
		Answers: p.Answers, RequestID: child.Interaction.RequestID,
	})
	if err == nil {
		_, err = d.handleSessionRespond(ctx, params)
	}
	if err != nil {
		if _, rerr := d.mesh.ReleaseNotice(ctx, n.ID, by, prev); rerr != nil {
			d.log.Warn("mesh: release notice failed", "id", n.ID, "err", rerr)
		}
		return err
	}
	d.meshChanged()
	return nil
}

// checkAnswer rejects an answer that does not fit the notice: a missing answer
// must never approve a prompt, and a mode change needs an option the notice offers.
func checkAnswer(p api.AgentRespondParams, i *session.Interaction) error {
	if i == nil {
		i = &session.Interaction{}
	}
	forms := 0
	for _, given := range []bool{p.Behavior != "", p.OptionValue != "", len(p.Answers) > 0} {
		if given {
			forms++
		}
	}
	if forms != 1 {
		return errors.New("give exactly one of allow|deny, an option, or answers")
	}
	switch {
	case p.Behavior != "":
		if p.Behavior != "allow" && p.Behavior != "deny" {
			return fmt.Errorf("behavior must be allow or deny, got %q", p.Behavior)
		}
		if i.Kind == session.InteractionQuestion && p.Behavior == "allow" {
			return errors.New("answer a question with answers, or decline it with deny")
		}
	case p.OptionValue != "":
		if !slices.ContainsFunc(i.Options, func(o session.DecisionOption) bool { return o.Value == p.OptionValue }) {
			return fmt.Errorf("%q is not an option of this notice", p.OptionValue)
		}
	default:
		if i.Kind != session.InteractionQuestion {
			return errors.New("answers apply only to a question prompt")
		}
		for q := range p.Answers {
			if !slices.ContainsFunc(i.Questions, func(s session.QuestionSpec) bool { return s.Question == q }) {
				return fmt.Errorf("%q is not a question of this notice", q)
			}
		}
	}
	return nil
}

func (d *Node) handleAgentKill(ctx context.Context, params json.RawMessage) (any, error) {
	p, caller, err := meshCall(ctx, d, params, func(p api.AgentTargetParams) api.CallerInfo { return p.Caller })
	if err != nil {
		return nil, err
	}
	return nil, d.kill(ctx, caller, p.SessionID)
}

func (d *Node) kill(ctx context.Context, caller session.Session, targetID string) error {
	target, err := d.localTarget(targetID)
	if err != nil {
		return err
	}
	key := partyKey(caller)
	by := "self"
	if key != partyKey(target) {
		link, ok, err := d.mesh.Link(ctx, partyKey(target))
		if err != nil {
			return err
		}
		if !ok || link.Parent.Key != key {
			return fmt.Errorf("only the parent of %s or the session itself can kill it", d.compositeID(target))
		}
		by = "parent"
	}
	if err := d.killSession(ctx, target); err != nil {
		return err
	}
	d.meshEnded(ctx, target, by)
	return nil
}
