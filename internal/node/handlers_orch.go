package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/orch"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
	"github.com/google/uuid"
)

var errOrchDisabled = errors.New("agent sessions are disabled on this node")

// localTarget is a conversation on this node: a live session, or a child this
// node spawned that has not started yet.
type localTarget struct {
	actor api.Actor
	s     session.Session
	live  bool
}

// resolveTarget resolves an agent ID (node, agent, agent session id) on this
// node. It never resolves a pane.
func (d *Node) resolveTarget(ctx context.Context, id string) (localTarget, error) {
	s, err := d.liveTarget(id)
	if err == nil {
		return localTarget{actor: d.actor(s), s: s, live: true}, nil
	}
	_, key, _ := session.SplitCompositeID(id)
	if spawning, serr := d.spawning(ctx, id); serr != nil {
		return localTarget{}, serr
	} else if spawning {
		return localTarget{actor: api.Actor{Node: d.id, Key: key}}, nil
	}
	return localTarget{}, err
}

// agentTarget resolves id to a live session; a child that has not started yet
// is not ready.
func (d *Node) agentTarget(ctx context.Context, id string) (session.Session, error) {
	t, err := d.resolveTarget(ctx, id)
	if err != nil {
		return session.Session{}, err
	}
	if !t.live {
		return session.Session{}, notStarted(id)
	}
	return t.s, nil
}

// agentKeyShaped reports whether key has the agent:id shape of an actor key.
// A tmux pane id starts with %, an agent session id never does.
func agentKeyShaped(key string) bool {
	agent, id, ok := strings.Cut(key, ":")
	return ok && agent != "" && id != "" && !strings.HasPrefix(id, "%")
}

// liveTarget is agentTarget without the not-ready case: a key that is not live
// is unknown.
func (d *Node) liveTarget(id string) (session.Session, error) {
	node, key, _ := session.SplitCompositeID(id)
	// An ID without the node prefix leaves a key that is not agent:id.
	if !agentKeyShaped(key) {
		return session.Session{}, d.missing(id)
	}
	if node != d.id {
		return session.Session{}, fmt.Errorf("session %s is on node %s, which is not connected", id, node)
	}
	if s, live := d.sessionForKey(key); live {
		return s, nil
	}
	return session.Session{}, d.missing(id)
}

// spawning reports whether id names a child this node spawned that it has not
// seen live yet, within the spawn window, and whose link has not ended.
func (d *Node) spawning(ctx context.Context, id string) (bool, error) {
	node, key, _ := session.SplitCompositeID(id)
	if d.orch == nil || node != d.id || !d.orchSpawning.pending(key, time.Now(), d.orchSpawnWindow) {
		return false, nil
	}
	l, ok, err := d.orch.Link(ctx, api.Actor{Node: d.id, Key: key})
	return err == nil && ok && l.EndedBy == "", err
}

// missing is the error for a target this node does not hold: not ready until
// startup discovery has settled, unknown after.
func (d *Node) missing(id string) error {
	if !d.settled() {
		return &api.RPCError{Code: api.CodeNotReady, Message: "node is still starting: " + id}
	}
	return &api.RPCError{Code: api.CodeUnknownSession, Message: "unknown session: " + id}
}

// settled reports whether startup discovery has settled. A node without the
// orch store has nothing to settle.
func (d *Node) settled() bool {
	if d.orchSettled == nil {
		return true
	}
	select {
	case <-d.orchSettled:
		return true
	default:
		return false
	}
}

// agentView returns s as orch.* clients see it: its ID is the agent ID.
func (d *Node) agentView(s session.Session) session.Session {
	s = d.withCaps(s)
	s.ID = d.agentID(s)
	return s
}

// agentID is the ID that orch.* clients use for s; "" until its agent session
// id is known.
func (d *Node) agentID(s session.Session) string {
	return actorID(d.actor(s))
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
	v, err := d.adapterFor(s.Agent).ReadTranscriptView(s.TranscriptPath)
	if err != nil {
		return v, err
	}
	v.Entries = markStaleWorkflows(v.Entries, d.reg.ProcStart(s.ID))
	return v, nil
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
	if caller.Local == nil {
		return session.Session{}, fmt.Errorf("current session: %s is not a session on this node", caller.name())
	}
	if d.agentID(*caller.Local) == "" {
		return session.Session{}, notReady(caller.Label)
	}
	return d.agentView(*caller.Local), nil
}

func (d *Node) handleOrchSessionList(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.OrchListParams](params)
	if err != nil {
		return nil, err
	}
	// A peer's request is never fanned out again.
	if _, onPeer := peerNodeFrom(ctx); p.All && !onPeer {
		return d.listAll(ctx), nil
	}
	return api.OrchListResult{Sessions: d.list()}, nil
}

// listPeerTimeout bounds how long listAll waits for each peer node.
const listPeerTimeout = 3 * time.Second

// listAll merges this node's sessions with those of every online peer. A peer
// that lacks cross-node support, errors, or is slow is reported in Unreachable.
func (d *Node) listAll(ctx context.Context) api.OrchListResult {
	peers := d.roster.others()
	results := make([][]session.Session, len(peers))
	failed := make([]bool, len(peers))
	var wg sync.WaitGroup
	for i, n := range peers {
		if !n.Online || n.Capabilities.CrossNodeOrch < 1 {
			failed[i] = true
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, listPeerTimeout)
			defer cancel()
			var res api.OrchListResult
			if err := d.forward(cctx, n.ID, api.MethodOrchSessionList, api.OrchListParams{}, &res); err != nil {
				failed[i] = true
				return
			}
			results[i] = res.Sessions
		}()
	}
	wg.Wait()
	out := api.OrchListResult{Sessions: d.list()}
	for i, n := range peers {
		if failed[i] {
			out.Unreachable = append(out.Unreachable, n.ID)
			continue
		}
		out.Sessions = append(out.Sessions, results[i]...)
	}
	return out
}

func (d *Node) list() []session.Session {
	out := []session.Session{}
	for _, s := range d.reg.Snapshot() {
		if d.agentID(s) != "" {
			out = append(out, d.agentView(s))
		}
	}
	return out
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
	if res, done, err := routed[api.OrchGetResult](ctx, d, id, api.MethodOrchSessionGet, api.OrchTargetParams{SessionID: id}); done {
		return res, err
	}
	s, err := d.agentTarget(ctx, id)
	if err != nil {
		return api.OrchGetResult{}, err
	}
	queued := []api.Message{}
	if key := actorKey(s); key != "" {
		if queued, err = d.orch.Queued(ctx, d.actor(s)); err != nil {
			return api.OrchGetResult{}, err
		}
	}
	return api.OrchGetResult{Session: d.agentView(s), Queued: queued}, nil
}

func (d *Node) handleOrchSessionOutput(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.OrchTargetParams](params)
	if err != nil {
		return nil, err
	}
	return d.output(ctx, p.SessionID)
}

func (d *Node) output(ctx context.Context, id string) (api.OrchOutputResult, error) {
	if res, done, err := routed[api.OrchOutputResult](ctx, d, id, api.MethodOrchSessionOutput, api.OrchTargetParams{SessionID: id}); done {
		return res, err
	}
	s, err := d.agentTarget(ctx, id)
	if err != nil {
		return api.OrchOutputResult{}, err
	}
	view, err := d.transcriptView(s)
	if err != nil {
		return api.OrchOutputResult{}, err
	}
	return api.OrchOutputResult{Text: lastAssistantText(view.Entries)}, nil
}

func (d *Node) handleOrchSessionTranscript(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.OrchTranscriptParams](params)
	if err != nil {
		return nil, err
	}
	return d.transcript(ctx, p.SessionID, p.Last)
}

func (d *Node) transcript(ctx context.Context, id string, last int) (api.OrchTranscriptResult, error) {
	if res, done, err := routed[api.OrchTranscriptResult](ctx, d, id, api.MethodOrchSessionTranscript, api.OrchTranscriptParams{SessionID: id, Last: last}); done {
		return res, err
	}
	s, err := d.agentTarget(ctx, id)
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
	// Agents read the raw prompt; the parsed copy would only cost tokens. A copy,
	// as the adapter may hand out its cached entries.
	out := make([]transcript.Entry, len(entries))
	for i, e := range entries {
		e.ArgusMessages = nil
		out[i] = e
	}
	return api.OrchTranscriptResult{Entries: out}, nil
}

func (d *Node) handleOrchSessionSpawn(ctx context.Context, params json.RawMessage) (any, error) {
	p, caller, err := orchCall(ctx, d, params, func(p api.OrchSpawnParams) api.CallerInfo { return p.Caller })
	if err != nil {
		return nil, err
	}
	return d.spawnFor(ctx, caller, p)
}

func (d *Node) spawnFor(ctx context.Context, caller callerRef, p api.OrchSpawnParams) (api.OrchSpawnResult, error) {
	// A peer's request is never forwarded again.
	if _, onPeer := peerNodeFrom(ctx); onPeer && p.Node != "" && p.Node != d.id {
		return api.OrchSpawnResult{}, fmt.Errorf("spawn on node %s was sent to node %s", p.Node, d.id)
	}
	nodeID, err := d.resolveNode(p.Node)
	if err != nil {
		return api.OrchSpawnResult{}, err
	}
	if nodeID != d.id {
		return d.spawnRemote(ctx, caller, nodeID, p)
	}
	if !filepath.IsAbs(p.Cwd) {
		return api.OrchSpawnResult{}, errors.New("cwd must be an absolute directory path")
	}
	if !d.orchestrated(p.Agent) {
		return api.OrchSpawnResult{}, fmt.Errorf("%s does not support agent sessions", p.Agent)
	}
	parent := caller.Actor
	if parent.Key == "" {
		return api.OrchSpawnResult{}, notReady(caller.name())
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
			ID: orch.NewID(d.id), Kind: api.MessageTask, From: parent, Body: p.Prompt,
			FromLabel: caller.Label, State: api.MessageDelivered,
		}
		prompt = orch.RenderPrompt([]orch.Delivery{{Msg: task, From: caller.Label, FromSession: actorID(caller.Actor)}})
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
	link := api.Link{Child: child, Parent: parent, TaskID: task.ID}
	if err := d.orch.LinkSpawned(ctx, link, task, req); err != nil {
		d.abortSpawn(ctx, p.Agent, childKey, spawned)
		return api.OrchSpawnResult{}, err
	}
	d.orchSpawning.add(childKey, time.Now())
	d.orchChanged()
	return api.OrchSpawnResult{SessionID: actorID(child), TaskID: task.ID}, nil
}

// spawnRemote spawns on nodeID, which stores the link and dedupes request_id,
// then mirrors the link here.
func (d *Node) spawnRemote(ctx context.Context, caller callerRef, nodeID string, p api.OrchSpawnParams) (api.OrchSpawnResult, error) {
	parent := caller.Actor
	if parent.Key == "" {
		return api.OrchSpawnResult{}, notReady(caller.name())
	}
	fwd := p
	fwd.Caller, fwd.Node = caller.remote(), ""
	var res api.OrchSpawnResult
	if err := d.forward(ctx, nodeID, api.MethodOrchSessionSpawn, fwd, &res); err != nil {
		// The target can still finish the spawn. A retry with the same
		// request_id returns that child, and this node then mirrors its link.
		if errors.Is(err, errPeerCallTimeout) {
			return api.OrchSpawnResult{}, fmt.Errorf("%w: the child can still start on node %s; retry with the same request_id to get it", err, nodeID)
		}
		return api.OrchSpawnResult{}, err
	}
	childNode, childKey, _ := session.SplitCompositeID(res.SessionID)
	if childNode != nodeID || !agentKeyShaped(childKey) {
		d.log.Warn("orch: remote spawn returned no agent id; link not mirrored", "node", nodeID, "session", res.SessionID)
		return res, nil
	}
	link := api.Link{Child: api.Actor{Node: nodeID, Key: childKey}, Parent: parent, TaskID: res.TaskID}
	// A request_id retry finds its mirror in place; rewriting it would clear ended_by.
	if prev, ok, err := d.orch.Link(ctx, link.Child); err == nil && ok && prev.Parent == parent {
		return res, nil
	}
	// The child is already running on nodeID, so a failed mirror write does not
	// fail the spawn: the result is returned and the error only logged.
	if err := d.orch.PutLink(context.WithoutCancel(ctx), link); err != nil {
		d.log.Warn("orch: mirror remote link failed", "node", nodeID, "child", link.Child.Key, "err", err)
		return res, nil
	}
	d.orchChanged()
	return res, nil
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

// send queues text from the caller to the session targetID. The caller's node
// owns the message: a message to a session on another node is stored here and
// delivered through the outbox, and that node reports its state back.
func (d *Node) send(ctx context.Context, caller callerRef, targetID, text, requestID string) (api.OrchSendResult, error) {
	if node, remote := d.ownerOf(targetID); remote {
		// A peer's call is never forwarded again.
		if _, onPeer := peerNodeFrom(ctx); onPeer {
			return api.OrchSendResult{}, fmt.Errorf("session %s is not on node %s", targetID, d.id)
		}
		// A retry finds its first message before it needs node to answer.
		if requestID != "" && caller.Actor.Key != "" {
			m, _, ok, err := d.orch.MessageByRequest(ctx, caller.Actor, requestID)
			if err != nil {
				return api.OrchSendResult{}, err
			}
			if ok {
				if m.To.Node != node || m.Body != text {
					return api.OrchSendResult{}, errRequestReused(requestID)
				}
				return api.OrchSendResult{MessageID: m.ID}, nil
			}
		}
		to, err := d.remoteActor(ctx, caller, node, targetID)
		if err != nil {
			return api.OrchSendResult{}, err
		}
		return d.sendTo(ctx, caller, to, text, requestID)
	}
	return d.sendLocal(ctx, caller, targetID, text, requestID)
}

// sendLocal queues text to targetID on this node. A spawned child that has not
// started yet gets the message once it starts, and the gone pass fails it if
// the child does not start within the spawn window.
func (d *Node) sendLocal(ctx context.Context, caller callerRef, targetID, text, requestID string) (api.OrchSendResult, error) {
	t, err := d.resolveTarget(ctx, targetID)
	if err != nil {
		return api.OrchSendResult{}, err
	}
	if t.live {
		return d.sendText(ctx, caller, t.s, targetID, text, requestID)
	}
	return d.sendTo(ctx, caller, t.actor, text, requestID)
}

// remoteActor names the session targetID on node by its actor key. While node
// answers, it asks node, so a session that is not running fails at once. While
// node is offline, a local link that records the session names it, unless the
// link says that session ended.
func (d *Node) remoteActor(ctx context.Context, caller callerRef, node, targetID string) (api.Actor, error) {
	if n, ok := d.roster.get(node); !ok || !n.Online {
		links, err := d.orch.Links(ctx)
		if err != nil {
			return api.Actor{}, err
		}
		for _, l := range links {
			switch {
			case l.Child.Node == node && actorID(l.Child) == targetID:
				if l.EndedBy != "" {
					return api.Actor{}, fmt.Errorf("%s is not running", targetID)
				}
				return l.Child, nil
			case l.Parent.Node == node && actorID(l.Parent) == targetID:
				if l.ParentEnded {
					return api.Actor{}, fmt.Errorf("%s is not running", targetID)
				}
				return l.Parent, nil
			}
		}
	}
	_, key, _ := session.SplitCompositeID(targetID)
	var got api.OrchGetResult
	if err := d.forward(ctx, node, api.MethodOrchSessionGet, api.OrchTargetParams{Caller: caller.remote(), SessionID: targetID}, &got); err != nil {
		// node holds the message for a linked child that is not live yet, and
		// fails it if the child never starts.
		var rpcErr *api.RPCError
		if errors.As(err, &rpcErr) && rpcErr.Code == api.CodeNotReady && agentKeyShaped(key) {
			return api.Actor{Node: node, Key: key}, nil
		}
		return api.Actor{}, err
	}
	if !d.orchestrated(got.Session.Agent) {
		return api.Actor{}, fmt.Errorf("%s does not support agent messages", targetID)
	}
	return api.Actor{Node: node, Key: key}, nil
}

// sendText queues text from the caller to target, a session on this node.
func (d *Node) sendText(ctx context.Context, caller callerRef, target session.Session, targetID, text, requestID string) (api.OrchSendResult, error) {
	if !d.orchestrated(target.Agent) {
		return api.OrchSendResult{}, fmt.Errorf("%s does not support agent messages", targetID)
	}
	return d.sendTo(ctx, caller, d.actor(target), text, requestID)
}

// sendTo stores text from the caller to the actor to, and queues its delivery
// to the node of to when that is another node.
func (d *Node) sendTo(ctx context.Context, caller callerRef, to api.Actor, text, requestID string) (api.OrchSendResult, error) {
	from := caller.Actor
	if from.Key == "" {
		return api.OrchSendResult{}, notReady(caller.name())
	}
	var req orch.Request
	if requestID != "" {
		req = newRequest(requestID, to.Node, to.Key, text)
		defer d.orchRequests.lock(from, req.ID)()
		if res, ok, err := d.sentRequest(ctx, from, req); err != nil || ok {
			return res, err
		}
	}
	msg := api.Message{
		Kind: api.MessageText, From: from, FromLabel: caller.Label, To: to,
		Body: text, State: api.MessageQueued,
	}
	var m api.Message
	var err error
	if to.Node == d.id {
		m, err = d.orch.InsertRequest(ctx, msg, req)
	} else {
		m, err = d.orch.InsertRequestAndEnqueue(ctx, msg, req, to.Node, api.MethodOrchPeerDeliver, func(m api.Message) any {
			return api.OrchPeerDeliverParams{Message: m}
		})
	}
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
	// A copy here addressed to the caller is replied to here, whichever node
	// created it: the reply is queued for the sender's node even while it is offline.
	orig, found, err := d.message(ctx, messageID)
	if err != nil {
		return api.OrchSendResult{}, err
	}
	if !found || orig.To != caller.Actor {
		fwd := api.OrchReplyParams{Caller: caller.remote(), MessageID: messageID, Text: text}
		if res, done, err := routedMessage[api.OrchSendResult](ctx, d, messageID, api.MethodOrchMessageReply, fwd); done {
			return res, err
		}
	}
	if !found {
		return api.OrchSendResult{}, fmt.Errorf("unknown message: %s", messageID)
	}
	if caller.Actor.Key == "" || caller.Actor != orig.To {
		return api.OrchSendResult{}, fmt.Errorf("only the recipient of %s can reply to it", messageID)
	}
	owner := api.Actor{Node: d.id, Key: api.ActorOwner}
	if orig.From.Key == api.ActorArgus {
		return api.OrchSendResult{}, errors.New("argus messages cannot be replied to: answer an interaction with the respond_to_interaction tool or argus session respond")
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
// replied. A reply to a sender on another node is queued for that node, which
// delivers it once it is back.
func (d *Node) insertReply(ctx context.Context, caller callerRef, orig api.Message, text string, state api.MessageState) (api.Message, error) {
	if to := orig.From; to.Node == d.id && !reservedKey(to.Key) {
		if _, live := d.sessionForKey(to.Key); !live {
			return api.Message{}, fmt.Errorf("the sender of %s is not running; the reply cannot be delivered", orig.ID)
		}
	} else if to.Node != d.id {
		// Of a sender on another node, only an ended parent is known here.
		if l, ok, err := d.orch.Link(ctx, caller.Actor); err == nil && ok && l.Parent == to && l.ParentEnded {
			return api.Message{}, fmt.Errorf("the sender of %s is not running; the reply cannot be delivered", orig.ID)
		}
	}
	reply := api.Message{
		Kind: api.MessageReply, From: caller.Actor, FromLabel: caller.Label,
		To: orig.From, Body: text, ReplyTo: orig.ID, State: state,
	}
	var m api.Message
	var open bool
	var err error
	if orig.From.Node != d.id {
		m, open, err = d.orch.ReplyAndEnqueue(ctx, orig.ID, reply, orig.From.Node)
	} else {
		m, open, err = d.orch.ReplyTo(ctx, orig.ID, reply)
	}
	if err != nil {
		return api.Message{}, err
	}
	if !open {
		return api.Message{}, fmt.Errorf("%s is no longer open; the reply cannot be sent", orig.ID)
	}
	d.pushState(ctx, orig, api.MessageReplied)
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
	fwd := p
	fwd.Caller = caller.remote()
	by := caller.Actor
	if _, done, err := routedMessage[struct{}](ctx, d, p.MessageID, api.MethodOrchInteractionRespond, fwd); done {
		if err == nil {
			d.claimCopy(ctx, p.MessageID, by)
		}
		return err
	}
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
				who = fmt.Sprintf("%s (%s)", sessionLabel(c), d.agentID(c))
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
		return fmt.Errorf("only the parent of %s can answer its prompts", d.agentID(child))
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
	if n.To.Node != d.id && caller.Actor.Node == n.To.Node {
		// The caller's node claims its copy when this call returns. It also
		// gets this push, for a call that fails on the way back.
		if err := d.orch.Enqueue(context.WithoutCancel(ctx), n.To.Node, api.MethodOrchPeerWithdraw, api.OrchPeerWithdrawParams{MessageID: n.ID, AnsweredBy: &by}); err != nil {
			d.log.Warn("orch: queue answered copy failed", "id", n.ID, "node", n.To.Node, "err", err)
		}
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

// claimCopy marks this node's copy of a notice another node owns answered by
// by, after that node took the answer.
func (d *Node) claimCopy(ctx context.Context, id string, by api.Actor) {
	if _, claimed, err := d.orch.ClaimInteraction(ctx, id, by); err != nil {
		d.log.Warn("orch: claim interaction copy failed", "id", id, "err", err)
	} else if claimed {
		d.orchChanged()
	}
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
	if res, done, err := routed[api.SessionRenameResult](ctx, d, id, api.MethodOrchSessionRename, api.OrchRenameParams{SessionID: id, Name: name}); done {
		return res, err
	}
	s, err := d.agentTarget(ctx, id)
	if err != nil {
		return api.SessionRenameResult{}, err
	}
	return d.renameSession(ctx, s, name)
}

func (d *Node) handleOrchSessionKill(ctx context.Context, params json.RawMessage) (any, error) {
	p, caller, err := orchCall(ctx, d, params, func(p api.OrchKillParams) api.CallerInfo { return p.Caller })
	if err != nil {
		return nil, err
	}
	return d.killAs(ctx, caller, p.SessionID, p.Ancestor)
}

func (d *Node) kill(ctx context.Context, caller callerRef, targetID string) (api.OrchKillResult, error) {
	return d.killAs(ctx, caller, targetID, false)
}

// killAs kills targetID for caller. ancestor marks a kill by another node of a
// child of a session it is killing: the child ends by "ancestor", as a local
// descendant does. Only a parent on another node can ask for it.
func (d *Node) killAs(ctx context.Context, caller callerRef, targetID string, ancestor bool) (api.OrchKillResult, error) {
	params := api.OrchKillParams{OrchTargetParams: api.OrchTargetParams{Caller: caller.remote(), SessionID: targetID}, Ancestor: ancestor}
	if res, done, err := routed[api.OrchKillResult](ctx, d, targetID, api.MethodOrchSessionKill, params); done {
		return res, err
	}
	t, err := d.resolveTarget(ctx, targetID)
	if err != nil {
		return api.OrchKillResult{}, err
	}
	actor := t.actor
	by := "self"
	if caller.Actor.Key == "" || caller.Actor != actor {
		link, ok, err := d.orch.Link(ctx, actor)
		if err != nil {
			return api.OrchKillResult{}, err
		}
		if !ok || caller.Actor.Key == "" || link.Parent != caller.Actor {
			return api.OrchKillResult{}, fmt.Errorf("only the parent of %s or the session itself can kill it", actorID(actor))
		}
		by = "parent"
		if ancestor && caller.Local == nil && caller.Actor.Node != d.id {
			by = "ancestor"
		}
	}
	if !t.live {
		return api.OrchKillResult{Killed: []string{d.doomSpawning(ctx, actor.Key, by)}}, nil
	}
	killed, notKilled, err := d.killTree(ctx, t.s, by)
	if err != nil {
		if len(killed) > 0 {
			err = fmt.Errorf("%w (killed: %s)", err, strings.Join(killed, ", "))
		}
		if len(notKilled) > 0 {
			err = fmt.Errorf("%w (not killed: %s)", err, strings.Join(notKilled, ", "))
		}
		return api.OrchKillResult{}, err
	}
	return api.OrchKillResult{Killed: killed, NotKilled: notKilled}, nil
}
