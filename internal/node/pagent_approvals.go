package node

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/agentgit"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/policy"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// Options of an approval card.
const (
	optAllowOnce   = "allow_once"
	optAllowThread = "allow_thread"
	optAllowAlways = "allow_always"
	optDeny        = "deny"
	optExplain     = "explain"
)

// Timeline events of drafts.
const (
	evDraft          = "draft"
	evDraftSent      = "draft.sent"
	evDraftDiscarded = "draft.discarded"
)

// Messages the agent CLI shows the model for an answer Argus gives.
const (
	reasonNoAnswer   = "no answer from the user"
	reasonNewMessage = "user sent a new message"
	reasonExplain    = "the user asks: explain in plain language what this does and why, then ask again"
	reasonUserDenied = "denied by the user"
	reasonRuleDenied = "denied by an approval rule"
	reasonOpaque     = "Argus cannot see what this action does, so unattended work needs your answer"
)

const (
	defaultUnattendedExpiry = 30 * time.Minute
	globalApprovalsFile     = "approvals.yaml"
	expiredKeep             = 24 * time.Hour
)

var errRequestExpired = errors.New("request expired")

var approvalOptions = []session.DecisionOption{
	{Label: "Allow once", Value: optAllowOnce},
	{Label: "Allow in this thread", Value: optAllowThread},
	{Label: "Always allow", Value: optAllowAlways},
	{Label: "Deny", Value: optDeny, Reject: true, Placeholder: "Tell the agent why"},
	{Label: "Explain", Value: optExplain},
}

// approvalState holds what the approval policy keeps in memory: thread grants,
// thread denials and the open cards.
type approvalState struct {
	mu sync.Mutex
	// grants and denied hold policy.Action GrantKey and DenyKey values by
	// thread.
	grants map[string]map[string]struct{}
	denied map[string]map[string]struct{}
	// once holds one-shot grants by thread: an approved draft, used by the
	// first matching request.
	once map[string]map[string]struct{}
	// cards are the open cards by cardKey.
	cards map[string]*approvalCard
	// expired holds the cardKey of cards that expired, for late answers.
	expired map[string]time.Time
	// seen is the card key of the request last looked at per headless
	// session, so each request is evaluated once.
	seen map[string]string
	// threadOf maps the sessions approvals were decided for to their thread.
	threadOf map[string]string
	// deciding counts headless requests being decided.
	deciding sync.WaitGroup
}

// approvalScope is an agent thread session an action comes from.
type approvalScope struct {
	agent     pagent.Agent
	thread    pagent.Thread
	sessionID string
	cli       string
}

// approvalCard is an open "ask" card of an agent session.
type approvalCard struct {
	key       string
	scope     approvalScope
	requestID string
	action    policy.Action
	// headless cards are answered through the adapter's Responder; the
	// others are parked hook calls.
	headless bool
	// hook is the shadow hook answer, for the activity record.
	hook string
	// closed takes a deny reason that ends a parked card.
	closed chan string
	timer  *time.Timer
}

func cardKey(sessionID, requestID string) string { return sessionID + "\x00" + requestID }

func (st *approvalState) init() {
	if st.cards == nil {
		st.grants = map[string]map[string]struct{}{}
		st.denied = map[string]map[string]struct{}{}
		st.once = map[string]map[string]struct{}{}
		st.cards = map[string]*approvalCard{}
		st.expired = map[string]time.Time{}
		st.seen = map[string]string{}
		st.threadOf = map[string]string{}
	}
}

func addKey(m map[string]map[string]struct{}, thread, key string) {
	if m[thread] == nil {
		m[thread] = map[string]struct{}{}
	}
	m[thread][key] = struct{}{}
}

func copyKeys(m map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}

// approvalScopeFor returns the agent thread session s is, if it is one.
func (d *Node) approvalScopeFor(ctx context.Context, s session.Session) (approvalScope, bool) {
	if d.pagents == nil {
		return approvalScope{}, false
	}
	agentID, threadID := d.pagentTag(s)
	if threadID == "" {
		return approvalScope{}, false
	}
	th, ok, err := d.pagents.Thread(ctx, threadID)
	if err != nil || !ok {
		return approvalScope{}, false
	}
	a, ok, err := d.pagents.AgentByID(ctx, agentID)
	if err != nil || !ok {
		return approvalScope{}, false
	}
	d.approvals.mu.Lock()
	d.approvals.init()
	d.approvals.threadOf[s.ID] = th.ID
	d.approvals.mu.Unlock()
	return approvalScope{agent: a, thread: th, sessionID: s.ID, cli: s.Agent}, true
}

// globalApprovalRules reads the node's approvals.yaml in the profile
// directory: a list of rules, or one under an approvals key. A missing file
// has none.
func (d *Node) globalApprovalRules() []policy.Rule {
	if d.pagentCfg.ProfileDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(d.pagentCfg.ProfileDir, globalApprovalsFile))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			d.log.Warn("pagent: read approvals", "err", err)
		}
		return nil
	}
	var doc struct {
		Approvals []policy.Rule `yaml:"approvals"`
	}
	if yaml.Unmarshal(data, &doc) == nil && doc.Approvals != nil {
		return doc.Approvals
	}
	var list []policy.Rule
	if err := yaml.Unmarshal(data, &list); err != nil {
		d.log.Warn("pagent: parse approvals", "err", err)
		return nil
	}
	return list
}

// verdict is the policy's decision on one action.
type verdict struct {
	action policy.Action
	dec    policy.Decision
	// hook is the policy hook's answer in shadow mode, for the activity
	// record.
	hook string
}

// approvalDecide describes and decides a tool request of an agent session.
func (d *Node) approvalDecide(ctx context.Context, sc approvalScope, tool string, input json.RawMessage) verdict {
	a := policy.Describe(sc.cli, tool, input)
	a.StartedBy, a.AgentID, a.ThreadID, a.SessionID = sc.thread.StartedBy, sc.agent.ID, sc.thread.ID, sc.sessionID
	in := policy.DecideInput{
		StartedBy:   sc.thread.StartedBy,
		Execution:   d.pagentCfg.Execution,
		GlobalRules: d.globalApprovalRules(),
		Now:         d.pagentNow(),
	}
	if def, err := pagent.LoadDefinition(sc.agent.Dir); err == nil {
		in.AgentRules = def.Approvals
	} else {
		d.log.Warn("pagent: read agent approvals", "agent", sc.agent.ID, "err", err)
	}
	v := verdict{action: a}
	if h := d.approvalHook(ctx, sc, a); h != nil {
		in.Hook = h
		if h.Mode == policy.HookShadow {
			v.hook = strings.TrimSpace(h.Decision + " " + h.Reason)
		}
	}
	key := a.GrantKey()
	d.approvals.mu.Lock()
	d.approvals.init()
	in.Denied = copyKeys(d.approvals.denied[sc.thread.ID])
	in.Grants = copyKeys(d.approvals.grants[sc.thread.ID])
	_, once := d.approvals.once[sc.thread.ID][key]
	if once {
		in.Grants[key] = struct{}{}
	}
	d.approvals.mu.Unlock()
	v.dec = policy.Decide(a, in)
	// Argus can't classify an opaque action, so it may be outward: a rule,
	// grant or policy can't let unattended work run it unseen.
	if v.dec.Outcome == policy.OutcomeAllow && sc.thread.StartedBy != policy.StartedByYou && policy.Opaque(a) {
		v.dec = policy.Decision{Outcome: policy.OutcomeAsk, By: policy.ByFloor, Reason: reasonOpaque}
	}
	if once && v.dec.By == policy.ByGrant {
		d.approvals.mu.Lock()
		if _, kept := d.approvals.grants[sc.thread.ID][key]; !kept {
			delete(d.approvals.once[sc.thread.ID], key)
		}
		d.approvals.mu.Unlock()
	}
	return v
}

// approvalImmediate answers a decision that needs no card: allow, deny or
// draft. A draft is recorded in the thread's timeline.
func (d *Node) approvalImmediate(ctx context.Context, sc approvalScope, v verdict) api.RespondParams {
	dec := v.dec
	p := api.RespondParams{Behavior: "allow"}
	switch dec.Outcome {
	case policy.OutcomeAllow:
	case policy.OutcomeDraft:
		d.recordDraft(ctx, sc, v.action)
		p = api.RespondParams{Behavior: "deny", Reason: policy.ReasonDraft}
	default:
		p = api.RespondParams{Behavior: "deny", Reason: cmp.Or(dec.Reason, reasonRuleDenied)}
	}
	d.recordActivity(ctx, sc, v.action, dec.Outcome, dec.By, dec.Rule, cmp.Or(p.Reason, dec.Reason), v.hook)
	return p
}

func (d *Node) recordDraft(ctx context.Context, sc approvalScope, a policy.Action) {
	payload := map[string]any{
		"tool":       a.Tool,
		"target":     a.Target,
		"classes":    a.Class,
		"session_id": sc.sessionID,
	}
	if len(a.Input) > 0 && json.Valid(a.Input) {
		payload["input"] = d.redactJSON(ctx, sc.agent.ID, a.Input)
	}
	if line, ok := a.ShellLine(); ok {
		payload["command"] = d.redact(ctx, sc.agent.ID, line)
	}
	if err := d.pagentEvent(ctx, sc.thread.ID, evDraft, sc.thread.StartedBy, payload, sc.sessionID, 0); err != nil {
		d.log.Warn("pagent: record draft", "thread", sc.thread.ID, "err", err)
	}
}

func (d *Node) unattendedExpiry() time.Duration {
	if e := d.pagentCfg.Approvals.UnattendedExpiry; e > 0 {
		return e
	}
	return defaultUnattendedExpiry
}

// openCard records an ask card. Cards from unattended work expire.
func (d *Node) openCard(ctx context.Context, sc approvalScope, requestID string, v verdict, headless bool) *approvalCard {
	c := &approvalCard{
		key: cardKey(sc.sessionID, requestID), scope: sc, requestID: requestID, action: v.action,
		headless: headless, hook: v.hook, closed: make(chan string, 1),
	}
	if sc.thread.StartedBy != policy.StartedByYou {
		d.notifyApproval(ctx, sc, v.action)
	}
	d.approvals.mu.Lock()
	defer d.approvals.mu.Unlock()
	d.approvals.init()
	d.approvals.cards[c.key] = c
	// A new request can reuse the key of an expired one (OpenCode requests
	// are keyed by content); answers go to the new card.
	delete(d.approvals.expired, c.key)
	if sc.thread.StartedBy != policy.StartedByYou {
		c.timer = time.AfterFunc(d.unattendedExpiry(), func() { d.expireCard(c) })
	}
	return c
}

// takeCard removes c from the open cards; false when it was already gone.
func (d *Node) takeCard(c *approvalCard) bool {
	d.approvals.mu.Lock()
	defer d.approvals.mu.Unlock()
	if d.approvals.cards[c.key] != c {
		return false
	}
	delete(d.approvals.cards, c.key)
	if c.timer != nil {
		c.timer.Stop()
	}
	return true
}

func (d *Node) expireCard(c *approvalCard) {
	if !d.takeCard(c) {
		return
	}
	d.approvals.mu.Lock()
	now := time.Now()
	for k, t := range d.approvals.expired {
		if now.Sub(t) > expiredKeep {
			delete(d.approvals.expired, k)
		}
	}
	d.approvals.expired[c.key] = now
	d.approvals.mu.Unlock()
	d.recordCard(c, policy.OutcomeDeny, decidedExpiry, "", reasonNoAnswer)
	d.endCard(c, reasonNoAnswer)
}

// endCard denies a card Argus closes: a parked one through its hook call, a
// headless one through the adapter.
func (d *Node) endCard(c *approvalCard, reason string) {
	if !c.headless {
		select {
		case c.closed <- reason:
		default:
		}
		return
	}
	go d.respondHeadless(context.Background(), c.scope.sessionID, c.requestID, api.RespondParams{Behavior: "deny", Reason: reason})
}

func (d *Node) cardExpired(sessionID, requestID string) bool {
	d.approvals.mu.Lock()
	defer d.approvals.mu.Unlock()
	_, ok := d.approvals.expired[cardKey(sessionID, requestID)]
	return ok
}

// approvalUserMessage applies a user message to a thread: open cards close
// and denials of earlier actions end.
func (d *Node) approvalUserMessage(threadID string) {
	d.approvals.mu.Lock()
	d.approvals.init()
	delete(d.approvals.denied, threadID)
	var open []*approvalCard
	for _, c := range d.approvals.cards {
		if c.scope.thread.ID == threadID {
			open = append(open, c)
		}
	}
	d.approvals.mu.Unlock()
	for _, c := range open {
		if d.takeCard(c) {
			d.recordCard(c, policy.OutcomeDeny, decidedUser, "", reasonNewMessage)
			d.endCard(c, reasonNewMessage)
		}
	}
}

// approvalSessionEnded drops the cards of an ended session and the grants of
// its thread.
func (d *Node) approvalSessionEnded(sessionID string) {
	d.approvals.mu.Lock()
	defer d.approvals.mu.Unlock()
	d.approvals.init()
	if th, ok := d.approvals.threadOf[sessionID]; ok {
		delete(d.approvals.grants, th)
		delete(d.approvals.threadOf, sessionID)
	}
	delete(d.approvals.seen, sessionID)
	for k, c := range d.approvals.cards {
		if c.scope.sessionID == sessionID {
			delete(d.approvals.cards, k)
			if c.timer != nil {
				c.timer.Stop()
			}
		}
	}
}

// approvalAnswer applies the user's answer to a card and returns the plain
// allow or deny the agent CLI gets.
func (d *Node) approvalAnswer(ctx context.Context, c *approvalCard, p api.RespondParams) api.RespondParams {
	out := api.RespondParams{SessionID: p.SessionID, RequestID: p.RequestID, Kind: p.Kind}
	opt := p.OptionValue
	if opt == "" {
		switch p.Behavior {
		case "allow":
			opt = optAllowOnce
		case "deny":
			opt = optDeny
		}
	}
	th := c.scope.thread.ID
	var rule, note string
	switch opt {
	case optAllowOnce, "allow":
		out.Behavior = "allow"
	case optAllowThread:
		note = "allowed for this thread"
		d.approvals.mu.Lock()
		d.approvals.init()
		addKey(d.approvals.grants, th, c.action.GrantKey())
		d.approvals.mu.Unlock()
		out.Behavior = "allow"
	case optAllowAlways:
		out.Behavior = "allow"
		r, err := d.alwaysAllow(ctx, c)
		if err != nil {
			d.log.Warn("pagent: save approval rule", "agent", c.scope.agent.ID, "err", err)
			note = "allowed once: " + err.Error()
			break
		}
		rule, note = r.String(), "always allowed"
	case optExplain:
		out.Behavior, out.Reason = "deny", reasonExplain
	default:
		d.approvals.mu.Lock()
		d.approvals.init()
		addKey(d.approvals.denied, th, c.action.DenyKey())
		d.approvals.mu.Unlock()
		out.Behavior, out.Reason = "deny", p.Reason
		if out.Reason == "" {
			out.Reason = reasonUserDenied
		}
	}
	d.recordCard(c, out.Behavior, decidedUser, rule, cmp.Or(out.Reason, note))
	return out
}

var errNoNarrowRule = errors.New("no rule covers only this action")

// alwaysAllow appends a narrow allow rule (tool and target) to the agent's
// approvals and commits it.
func (d *Node) alwaysAllow(ctx context.Context, c *approvalCard) (policy.Rule, error) {
	a, th := c.scope.agent, c.scope.thread.ID
	r, ok := policy.AlwaysRule(c.action)
	if !ok {
		return r, errNoNarrowRule
	}
	return r, d.saveAllowRule(ctx, a, th, r)
}

func (d *Node) saveAllowRule(ctx context.Context, a pagent.Agent, th string, r policy.Rule) error {
	defer lockMemory(a.ID)()
	repo, err := agentgit.Open(ctx, a.Dir, d.pagentGitMode())
	if err != nil {
		return err
	}
	if err := d.commitHandEdits(ctx, repo, a, th, []string{pagent.DefinitionFile}); err != nil {
		return err
	}
	def, err := pagent.LoadDefinition(a.Dir)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(def.Approvals, func(x policy.Rule) bool {
		return x.Tool == r.Tool && x.Target == r.Target && x.Class == "" && len(x.StartedBy) == 0 &&
			x.Decision == r.Decision && x.Expires.IsZero()
	}) {
		return nil
	}
	def.Approvals = append(def.Approvals, r)
	if err := def.Save(a.Dir); err != nil {
		return err
	}
	_, err = repo.CommitPaths(ctx, "approvals: always allow "+r.String(), memoryTrailers(a, startedByYou, th), []string{pagent.DefinitionFile}, true)
	if err == nil {
		d.notifyConns(api.MethodPAgentChanged)
	}
	return err
}

// enrichCard turns ix into an approval card.
func (d *Node) enrichCard(ctx context.Context, ix *session.Interaction, c *approvalCard) {
	agentID := c.scope.agent.ID
	ix.ToolInput = d.redact(ctx, agentID, ix.ToolInput)
	ix.Message = d.redact(ctx, agentID, ix.Message)
	ix.Options = slices.Clone(approvalOptions)
	if _, ok := policy.AlwaysRule(c.action); !ok {
		ix.Options = slices.DeleteFunc(ix.Options, func(o session.DecisionOption) bool { return o.Value == optAllowAlways })
	}
	ix.Classes = slices.Clone(c.action.Class)
	ix.StartedBy = c.scope.thread.StartedBy
}

// approvalOverlay shows a headless session's open card with the approval
// options. The adapter owns the interaction, so it is replaced in the copy
// handed to clients.
func (d *Node) approvalOverlay(s session.Session) session.Session {
	ix := s.Interaction
	if ix == nil || ix.Kind != session.InteractionPermission {
		return s
	}
	d.approvals.mu.Lock()
	c := d.approvals.cards[cardKey(s.ID, headlessKey(*ix))]
	d.approvals.mu.Unlock()
	if c == nil || !c.headless {
		return s
	}
	cp := *ix
	d.enrichCard(context.Background(), &cp, c)
	s.Interaction = &cp
	return s
}

// headlessKey identifies a headless request: its request id, else its
// content (OpenCode shows one permission at a time without an id).
func headlessKey(ix session.Interaction) string {
	if ix.RequestID != "" {
		return ix.RequestID
	}
	h := sha256.Sum256([]byte(ix.ToolName + "\x00" + ix.ToolInput + "\x00" + ix.Message))
	return "h:" + hex.EncodeToString(h[:8])
}

// answerable reports whether a permission interaction can be answered with
// allow or deny.
func answerable(ix session.Interaction) bool {
	var allow, deny bool
	for _, o := range ix.Options {
		allow = allow || o.Value == "allow"
		deny = deny || o.Value == "deny"
	}
	return allow && deny
}

// approvalTrack applies the approval policy to the approval requests of
// headless agent sessions as they show up, and forgets ended sessions.
func (d *Node) approvalTrack(ctx context.Context, ev registry.Event) {
	s := ev.Session
	if ev.Type == registry.EventRemoved || s.Status == session.StatusDead {
		d.approvalSessionEnded(s.ID)
		return
	}
	ix := s.Interaction
	if ix == nil || ix.Kind != session.InteractionPermission || !answerable(*ix) {
		d.approvals.mu.Lock()
		if d.approvals.seen != nil {
			delete(d.approvals.seen, s.ID)
		}
		d.approvals.mu.Unlock()
		d.dropHeadlessCards(s.ID, "")
		return
	}
	d.dropHeadlessCards(s.ID, headlessKey(*ix))
	if _, ok := d.adapterFor(s.Agent).(adapter.Responder); !ok {
		return
	}
	if ix.RequestID != "" {
		d.pendingMu.Lock()
		parked := d.pending[ix.RequestID] != nil
		d.pendingMu.Unlock()
		if parked {
			return
		}
	}
	key := headlessKey(*ix)
	d.approvals.mu.Lock()
	d.approvals.init()
	if d.approvals.seen[s.ID] == key {
		d.approvals.mu.Unlock()
		return
	}
	d.approvals.seen[s.ID] = key
	d.approvals.mu.Unlock()

	// The decision can wait on the policy hook; the registry event loop must
	// not, or it drops events such as session ends.
	d.approvals.deciding.Add(1)
	go func() {
		defer d.approvals.deciding.Done()
		d.approvalTrackDecide(ctx, s, key)
	}()
}

// approvalTrackDecide decides the request key that headless session s shows,
// unless the session moved on meanwhile.
func (d *Node) approvalTrackDecide(ctx context.Context, s session.Session, key string) {
	ix := s.Interaction
	sc, ok := d.approvalScopeFor(ctx, s)
	if !ok {
		return
	}
	v := d.approvalDecide(ctx, sc, ix.ToolName, json.RawMessage(ix.ToolInput))
	if !d.approvalStillSeen(s.ID, key) {
		return
	}
	if v.dec.Outcome == policy.OutcomeAsk {
		c := d.openCard(ctx, sc, key, v, true)
		if !d.approvalStillSeen(s.ID, key) {
			d.takeCard(c)
			return
		}
		d.reg.Republish(s.ID)
		return
	}
	p := d.approvalImmediate(ctx, sc, v)
	d.respondHeadless(context.Background(), s.ID, key, p)
}

// approvalStillSeen reports whether key is still the request session id
// shows; false once the request went away or the session ended.
func (d *Node) approvalStillSeen(id, key string) bool {
	d.approvals.mu.Lock()
	defer d.approvals.mu.Unlock()
	return d.approvals.seen != nil && d.approvals.seen[id] == key
}

// dropHeadlessCards forgets the headless cards of a session except keep: the
// request was answered in the CLI itself, or withdrawn.
func (d *Node) dropHeadlessCards(sessionID, keep string) {
	d.approvals.mu.Lock()
	defer d.approvals.mu.Unlock()
	for k, c := range d.approvals.cards {
		if c.headless && c.scope.sessionID == sessionID && c.requestID != keep {
			delete(d.approvals.cards, k)
			if c.timer != nil {
				c.timer.Stop()
			}
		}
	}
}

// respondHeadless answers a headless session's request with a plain allow or
// deny through its adapter.
func (d *Node) respondHeadless(ctx context.Context, sessionID, key string, p api.RespondParams) {
	s, ok := d.reg.Get(sessionID)
	if !ok {
		return
	}
	r, ok := d.adapterFor(s.Agent).(adapter.Responder)
	if !ok {
		return
	}
	p.SessionID, p.Kind = sessionID, string(session.InteractionPermission)
	if s.Interaction != nil && headlessKey(*s.Interaction) == key {
		p.RequestID = s.Interaction.RequestID
	} else if len(key) > 2 && key[:2] != "h:" {
		p.RequestID = key
	} else {
		// The request is no longer the one showing.
		return
	}
	if err := r.Respond(ctx, s, p); err != nil {
		d.log.Warn("pagent: answer approval", "session", sessionID, "err", err)
		return
	}
	if o, ok := r.(adapter.InteractionOwner); !ok || !o.OwnsInteraction() {
		d.reg.ClearInteraction(sessionID)
	}
}

// headlessAnswer turns the user's answer to a headless session's open card
// into the plain answer its adapter takes. ok is false when s shows no card.
func (d *Node) headlessAnswer(ctx context.Context, s session.Session, rid string, p api.RespondParams) (api.RespondParams, bool) {
	key := rid
	if key == "" && s.Interaction != nil {
		key = headlessKey(*s.Interaction)
	}
	if key == "" {
		return p, false
	}
	d.approvals.mu.Lock()
	c := d.approvals.cards[cardKey(s.ID, key)]
	d.approvals.mu.Unlock()
	if c == nil || !c.headless || !d.takeCard(c) {
		return p, false
	}
	out := d.approvalAnswer(ctx, c, p)
	out.RequestID = p.RequestID
	return out, true
}

// answerCard applies the user's answer to a parked card, unless Argus closed
// the card while the answer was on its way.
func (d *Node) answerCard(ctx context.Context, c *approvalCard, p api.RespondParams) api.RespondParams {
	if !d.takeCard(c) {
		select {
		case reason := <-c.closed:
			return api.RespondParams{Behavior: "deny", Reason: reason}
		default:
		}
	}
	return d.approvalAnswer(ctx, c, p)
}

// cardRequest is the card key part of the request a respond names: rid, else
// the request s shows.
func cardRequest(s session.Session, rid string) string {
	if rid == "" && s.Interaction != nil {
		return headlessKey(*s.Interaction)
	}
	return rid
}

// draftTimeout bounds a sent shell draft. A var so tests can shorten it.
var draftTimeout = 10 * time.Minute

const draftOutputMax = 64 << 10

// draftEvent returns the draft event id of thread, refusing one already sent
// or discarded.
func (d *Node) draftEvent(ctx context.Context, p api.PAgentDraftParams) (pagent.Thread, pagent.Agent, pagent.Event, error) {
	s, err := d.pagentStore()
	if err != nil {
		return pagent.Thread{}, pagent.Agent{}, pagent.Event{}, err
	}
	if p.ThreadID == "" || p.EventID <= 0 {
		return pagent.Thread{}, pagent.Agent{}, pagent.Event{}, pagentErr("thread_id and event_id are required")
	}
	th, ok, err := s.Thread(ctx, p.ThreadID)
	if err != nil {
		return th, pagent.Agent{}, pagent.Event{}, pagentErr("%v", err)
	}
	if !ok {
		return th, pagent.Agent{}, pagent.Event{}, pagentErr("thread not found")
	}
	a, err := d.pagentFind(ctx, s, th.AgentID)
	if err != nil {
		return th, a, pagent.Event{}, err
	}
	events, err := s.Events(ctx, th.ID, p.EventID-1)
	if err != nil {
		return th, a, pagent.Event{}, pagentErr("%v", err)
	}
	var draft pagent.Event
	for _, ev := range events {
		if ev.ID == p.EventID && ev.Kind == evDraft {
			draft = ev
		}
		if ev.Kind == evDraftSent || ev.Kind == evDraftDiscarded {
			var ref struct {
				EventID int64 `json:"event_id"`
			}
			if json.Unmarshal(ev.Payload, &ref) == nil && ref.EventID == p.EventID {
				return th, a, draft, pagentErr("draft already %s", map[string]string{evDraftSent: "sent", evDraftDiscarded: "discarded"}[ev.Kind])
			}
		}
	}
	if draft.ID == 0 {
		return th, a, draft, pagentErr("draft not found")
	}
	return th, a, draft, nil
}

type draftPayload struct {
	Tool    string          `json:"tool"`
	Target  string          `json:"target"`
	Input   json.RawMessage `json:"input"`
	Command string          `json:"command"`
	Classes []string        `json:"classes"`
}

// draftLocks keeps a draft from being sent twice at once.
var draftLocks sync.Map

// handlePAgentDraftSend does what a draft holds: a shell command runs again in
// the directory it came from, any other action goes to the agent as your message.
func (d *Node) handlePAgentDraftSend(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentDraftParams](params)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%s/%d", p.ThreadID, p.EventID)
	if _, busy := draftLocks.LoadOrStore(key, struct{}{}); busy {
		return nil, pagentErr("draft is being sent")
	}
	unlock := func() { draftLocks.Delete(key) }
	th, a, ev, err := d.draftEvent(ctx, p)
	if err != nil {
		unlock()
		return nil, err
	}
	var dp draftPayload
	if err := json.Unmarshal(ev.Payload, &dp); err != nil {
		unlock()
		return nil, pagentErr("bad draft: %v", err)
	}
	act := policy.Action{Tool: dp.Tool, Target: dp.Target, Input: dp.Input}
	if act.IsShell() {
		line, _ := act.ShellLine()
		if line = cmp.Or(dp.Command, line); line == "" {
			unlock()
			return nil, pagentErr("bad draft: no command")
		}
		go func() {
			defer unlock()
			d.runDraft(a, th.ID, ev.ID, line, draftDir(a, dp.Input))
		}()
		return struct{}{}, nil
	}
	defer unlock()
	send := api.PAgentSendParams{AgentID: a.ID, Text: "The user approved: " + secretRefs(draftAction(dp)) + ". Do it now."}
	if th.Kind == threadChat && th.Status == pagent.ThreadActive {
		send.ThreadID = th.ID
	} else if latest, ok, err := d.pagents.LatestThread(ctx, a.ID, threadChat); err == nil && ok {
		send.ThreadID = latest.ID
	}
	// The approved action runs once in the chat without asking again.
	act.Class = dp.Classes
	grant := act.GrantKey()
	if send.ThreadID != "" {
		d.grantOnce(send.ThreadID, grant)
	}
	raw, err := json.Marshal(send)
	if err != nil {
		return nil, err
	}
	res, err := d.handlePAgentThreadSend(ctx, raw)
	if err != nil {
		if send.ThreadID != "" {
			d.revokeOnce(send.ThreadID, grant)
		}
		return nil, err
	}
	payload := map[string]any{"event_id": ev.ID}
	if r, ok := res.(api.PAgentSendResult); ok {
		payload["thread_id"] = r.ThreadID
		if send.ThreadID == "" {
			d.grantOnce(r.ThreadID, grant)
		}
	}
	if err := d.pagentEvent(ctx, th.ID, evDraftSent, startedByYou, payload, "", 0); err != nil {
		return nil, pagentErr("%v", err)
	}
	return struct{}{}, nil
}

// draftAction describes a non-shell draft for the agent.
func draftAction(dp draftPayload) string {
	s := dp.Tool
	if len(dp.Input) > 0 && string(dp.Input) != "null" {
		s += " with input " + string(dp.Input)
	}
	return s
}

// draftDir is where a shell draft runs: the working directory its input
// names (Codex sends one), else the agent's directory.
func draftDir(a pagent.Agent, input json.RawMessage) string {
	var in struct {
		Workdir string `json:"workdir"`
		Cwd     string `json:"cwd"`
	}
	if json.Unmarshal(input, &in) == nil {
		if dir := cmp.Or(in.Workdir, in.Cwd); filepath.IsAbs(dir) {
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
				return dir
			}
		}
	}
	return a.Dir
}

// draftWaitDelay bounds the wait for a killed draft's output, when a child
// process keeps it open.
const draftWaitDelay = 5 * time.Second

// runDraft runs a shell draft in dir and records its output.
func (d *Node) runDraft(a pagent.Agent, threadID string, eventID int64, line, dir string) {
	ctx, cancel := context.WithTimeout(context.Background(), draftTimeout)
	defer cancel()
	secrets := d.agentSecrets(ctx, a.ID)
	cmd := exec.CommandContext(ctx, "sh", "-c", pagent.Unredact(line, secrets))
	cmd.Dir = dir
	cmd.WaitDelay = draftWaitDelay
	cmd.Env = os.Environ()
	for _, s := range secrets {
		cmd.Env = append(cmd.Env, s.Name+"="+s.Value)
	}
	out, err := cmd.CombinedOutput()
	out = []byte(pagent.Redact(string(out), secrets))
	if len(out) > draftOutputMax {
		out = append(out[:draftOutputMax:draftOutputMax], "\n[output truncated]"...)
	}
	payload := map[string]any{"event_id": eventID, "command": line, "dir": dir, "output": string(out)}
	if cmd.ProcessState != nil {
		payload["exit_code"] = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		payload["error"] = err.Error()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			payload["error"] = "timed out after " + draftTimeout.String()
		}
	}
	if err := d.pagentEvent(context.Background(), threadID, evDraftSent, startedByYou, payload, "", 0); err != nil {
		d.log.Warn("pagent: record sent draft", "thread", threadID, "err", err)
	}
}

func (d *Node) handlePAgentDraftDiscard(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentDraftParams](params)
	if err != nil {
		return nil, err
	}
	th, _, ev, err := d.draftEvent(ctx, p)
	if err != nil {
		return nil, err
	}
	if err := d.pagentEvent(ctx, th.ID, evDraftDiscarded, startedByYou, map[string]any{"event_id": ev.ID}, "", 0); err != nil {
		return nil, pagentErr("%v", err)
	}
	return struct{}{}, nil
}

func (d *Node) grantOnce(threadID, key string) {
	d.approvals.mu.Lock()
	defer d.approvals.mu.Unlock()
	d.approvals.init()
	addKey(d.approvals.once, threadID, key)
}

func (d *Node) revokeOnce(threadID, key string) {
	d.approvals.mu.Lock()
	defer d.approvals.mu.Unlock()
	if d.approvals.once != nil {
		delete(d.approvals.once[threadID], key)
	}
}

var secretMark = regexp.MustCompile(`«([A-Z_][A-Z0-9_]*)»`)

// secretRefs turns redaction marks into variable references for the agent.
func secretRefs(text string) string { return secretMark.ReplaceAllString(text, "$$$1") }
