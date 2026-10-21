package pi

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/gittree"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// spawnProc starts an owned pi under its relay; a var so tests can run the
// relay in process against a fake RPC peer.
var spawnProc = spawnHosted

// dismissKillAfter is the relay's grace (hostKillAfter) before it kills pi;
// dismiss waits a second past it for the relay to report the exit.
var dismissKillAfter = 5 * time.Second

// startExitWait bounds how long a spawn whose first call failed waits to see
// whether pi exited at start.
var startExitWait = 2 * time.Second

// pendingUI is one extension dialog awaiting a user decision: the Argus
// interaction surfaced for it, answered by extension_ui_response.
type pendingUI struct {
	uiID    string // extension_ui_request id
	method  string // select | confirm | input | editor
	title   string
	message string
	options []string
	inRun   bool // opened while a run was in progress
}

// card is the state a pi session's card shows, owned or foreign.
type card struct {
	mu        sync.Mutex
	id        string // pi session id (AgentSessionID); /new changes it
	prevID    string // the session id before a switch, until a push carries it
	cwd       string
	repo      string
	file      string // may be empty: pi creates it after the first user/assistant message
	name      string
	model     string
	reasoning bool // the model takes a thinking level
	level     string
	streaming bool
	pending   map[string]*pendingUI // ui request id -> dialog
	queue     []string              // prompts pi has not taken yet
}

func (c *card) sessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.id
}

// hookUpdate builds the card's push; c.mu must be held. Empty status leaves
// status/interaction alone; non-nil interaction replaces the current one.
func (c *card) hookUpdate(st session.Status, ix *session.Interaction, replace bool) registry.HookUpdate {
	return registry.HookUpdate{
		Agent:              Agent,
		AgentSessionID:     c.id,
		PrevAgentSessionID: c.prevID,
		Name:               c.name,
		Cwd:                c.cwd,
		Repo:               c.repo,
		TranscriptPath:     c.file,
		Frontend:           session.FrontendExternal,
		Input:              session.InputAPI,
		Status:             st,
		Interaction:        ix,
		ReplaceInteraction: replace,
		Model:              modelUpdate(c.model, c.reasoning, c.level),
	}
}

// pickPending picks the dialog to answer: by RequestID when the client echoes
// it, else the single pending dialog. Ambiguous state is an error rather than
// a guess at which question the user saw.
func (c *card) pickPending(requestID string) (*pendingUI, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var pend *pendingUI
	if requestID != "" {
		pend = c.pending[requestID]
	} else if len(c.pending) == 1 {
		for _, p := range c.pending {
			pend = p
		}
	}
	if pend == nil {
		return nil, fmt.Errorf("pi: no pending dialog for session %s", c.id)
	}
	return pend, nil
}

// dropPending removes a dialog that is no longer waiting. It reports whether
// that took down the last one.
func (c *card) dropPending(uiID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, had := c.pending[uiID]
	delete(c.pending, uiID)
	return had && len(c.pending) == 0
}

// sessionProc is one Argus-owned `pi --mode rpc` process, reached through
// its relay.
type sessionProc struct {
	card
	conn *rpcConn
}

// discoverer supervises owned pi RPC processes and mirrors them into the
// registry. Liveness is the relay connection: a session exists while its
// relay is connected. There is no shared daemon to poll (unlike
// Codex/OpenCode): a reaper per connection drops ended ones, and all other
// updates are event-driven.
type discoverer struct {
	reg *registry.Registry

	mu           sync.Mutex
	procs        map[string]*sessionProc // pi session id -> owned proc
	reattachOnce sync.Once

	// Beacon side (Tier 2): foreign sessions whose gate extension dialed in.
	foreign map[string]*foreignSession
	paneFor func(ctx context.Context, pid int) (session.TmuxServer, string)

	seq atomic.Uint64
}

func newDiscoverer(reg *registry.Registry, clients map[session.TmuxServer]*tmux.Client) *discoverer {
	return &discoverer{
		reg:     reg,
		procs:   map[string]*sessionProc{},
		foreign: map[string]*foreignSession{},
		paneFor: paneLookup(clients),
	}
}

func (d *discoverer) nextID() string {
	return fmt.Sprintf("argus-%d", d.seq.Add(1))
}

func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("pi-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func (d *discoverer) ScanOnce(ctx context.Context) error {
	d.reattachFirst(ctx)
	d.sweepForeign()
	return nil
}

// reattachFirst runs the one reattach. It outlives the request that happens
// to trigger it, since it never runs again, and a spawn waits for it so the
// listing never picks up the spawn's own relay.
func (d *discoverer) reattachFirst(ctx context.Context) {
	d.reattachOnce.Do(func() { d.reattach(context.WithoutCancel(ctx)) })
}

// reattach restores the owned sessions whose relays outlived the previous
// argusd. A socket that refuses the connection, or a file that is not a
// socket, is left over from a relay that died. Other dial errors keep the
// socket, since its relay can still be alive.
func (d *discoverer) reattach(ctx context.Context) {
	paths, _ := filepath.Glob(filepath.Join(hostDir(), "*.sock"))
	dialer := net.Dialer{Timeout: time.Second}
	for _, path := range paths {
		c, err := dialer.DialContext(ctx, "unix", path)
		if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOTSOCK) {
			os.Remove(path)
		}
		if err != nil {
			continue
		}
		d.adopt(ctx, newRPCConn(c, c))
	}
}

// adopt tracks a running pi from its relay. The relay replays open dialogs
// before get_state answers, so events wait until the card has its id.
func (d *discoverer) adopt(ctx context.Context, conn *rpcConn) {
	p := &sessionProc{card: card{pending: map[string]*pendingUI{}}, conn: conn}
	type event struct {
		typ string
		raw json.RawMessage
	}
	var (
		gate  sync.Mutex
		ready bool
		held  []event
	)
	conn.start(func(typ string, raw json.RawMessage) {
		gate.Lock()
		defer gate.Unlock()
		if !ready {
			held = append(held, event{typ, raw})
			return
		}
		d.procEvent(p, typ, raw)
	})
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	data, err := conn.call(sctx, d.nextID(), "get_state", nil)
	cancel()
	var st rpcState
	if err == nil {
		err = json.Unmarshal(data, &st)
	}
	if err != nil || st.SessionID == "" {
		conn.close()
		return
	}
	conn.mu.Lock()
	cwd := conn.hostCwd
	conn.mu.Unlock()
	p.id, p.cwd, p.repo, p.streaming = st.SessionID, cwd, gittree.RepoName(cwd), st.IsStreaming
	p.foldState(st)
	d.mu.Lock()
	if d.procs[p.id] != nil {
		d.mu.Unlock()
		conn.close()
		return
	}
	d.procs[p.id] = p
	d.mu.Unlock()
	go d.reap(p)

	gate.Lock()
	for _, e := range held {
		d.procEvent(p, e.typ, e.raw)
	}
	ready = true
	gate.Unlock()

	p.mu.Lock()
	asking := len(p.pending) > 0
	p.mu.Unlock()
	switch {
	case asking:
	case st.IsStreaming:
		d.upsert(p, session.StatusWorking, nil, false)
	default:
		d.upsert(p, session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle}, true)
	}
}

// reap drops p once its relay connection ends, unless dismiss took it first.
func (d *discoverer) reap(p *sessionProc) {
	<-p.conn.done
	p.conn.close()
	d.mu.Lock()
	still := d.procs[p.sessionID()] == p
	d.mu.Unlock()
	if still {
		d.forget(p)
	}
}

// running reports whether a pi process runs session id, owned or foreign.
func (d *discoverer) running(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.procs[id] != nil || d.foreign[id] != nil
}

func (d *discoverer) get(id string) *sessionProc {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.procs[id]
}

// spawnSession starts an owned pi RPC process in cwd and returns the pi
// session id. A non-empty prompt is sent after the process is tracked, so a
// send failure still leaves a live, listed session.
func (d *discoverer) spawnSession(ctx context.Context, cwd string, opts adapter.SpawnOptions) (string, error) {
	d.reattachFirst(ctx)
	gate, err := gateExtension()
	if err != nil {
		return "", err
	}
	id := newSessionID()
	// The process outlives the request: a request's context ends with the
	// client connection that sent it.
	conn, err := spawnProc(context.WithoutCancel(ctx), cwd, id, gate, opts.Model)
	if err != nil {
		return "", err
	}
	p := &sessionProc{
		card: card{id: id, cwd: cwd, repo: gittree.RepoName(cwd), pending: map[string]*pendingUI{}},
		conn: conn,
	}
	conn.start(func(typ string, raw json.RawMessage) { d.procEvent(p, typ, raw) })
	d.mu.Lock()
	d.procs[id] = p
	d.mu.Unlock()
	go d.reap(p)

	// Hydrate identity: session file, name, and model. pi exits at start on a
	// model it cannot resolve.
	if _, err := d.loadState(ctx, p); err != nil {
		select {
		case <-conn.done:
			if ex, ok := conn.exit(); ok && ex.Stderr != "" {
				return "", fmt.Errorf("pi: exited at start: %s", ex.Stderr)
			}
			return "", fmt.Errorf("pi: exited at start: %w", err)
		case <-time.After(startExitWait):
		}
	}
	id = p.sessionID()
	d.upsert(p, session.StatusWorking, nil, false)
	if opts.Prompt != "" {
		if err := d.sendPrompt(ctx, id, opts.Prompt); err != nil {
			return id, err
		}
	}
	return id, nil
}

// rpcState is the slice of get_state the card needs.
type rpcState struct {
	SessionID     string `json:"sessionId"`
	SessionFile   string `json:"sessionFile"`
	SessionName   string `json:"sessionName"`
	ThinkingLevel string `json:"thinkingLevel"`
	IsStreaming   bool   `json:"isStreaming"`
	Model         *struct {
		ID        string `json:"id"`
		Reasoning bool   `json:"reasoning"`
	} `json:"model"`
}

// loadState folds get_state into p and reports whether the card changed. Pi
// emits no event when the model changes, so this also runs after every settled
// run.
func (d *discoverer) loadState(ctx context.Context, p *sessionProc) (bool, error) {
	id := p.sessionID()
	data, err := p.conn.call(ctx, d.nextID(), "get_state", nil)
	if err != nil {
		return false, err
	}
	var st rpcState
	if err := json.Unmarshal(data, &st); err != nil {
		return false, err
	}
	d.mu.Lock()
	p.mu.Lock()
	defer p.mu.Unlock()
	// A concurrent get_state moved p, or p exited or was dismissed, since this
	// one was asked.
	if p.id != id || d.procs[id] != p {
		d.mu.Unlock()
		return false, nil
	}
	// The process moved to another session (/new, or an extension switching
	// sessions): the card follows it to the new id.
	rekeyed := st.SessionID != "" && st.SessionID != id
	if rekeyed {
		// A switch no push has carried yet keeps the id the record still has.
		if p.prevID == "" {
			p.prevID = id
		}
		p.id, p.name, p.file, p.queue, p.pending = st.SessionID, "", "", nil, map[string]*pendingUI{}
		delete(d.procs, id)
		d.procs[st.SessionID] = p
	}
	d.mu.Unlock()
	return p.foldStateLocked(st) || rekeyed, nil
}

func (c *card) foldState(st rpcState) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.foldStateLocked(st)
}

// foldStateLocked takes get_state's file, name, and model; c.mu must be held.
// It reports whether any changed.
func (c *card) foldStateLocked(st rpcState) bool {
	file, name, model, reasoning, level := c.file, c.name, c.model, c.reasoning, c.level
	if st.SessionFile != "" {
		c.file = st.SessionFile
	}
	if st.SessionName != "" {
		c.name = st.SessionName
	}
	if st.Model != nil && st.Model.ID != "" {
		c.model, c.reasoning, c.level = st.Model.ID, st.Model.Reasoning, st.ThinkingLevel
	}
	return c.file != file || c.name != name || c.model != model || c.reasoning != reasoning || c.level != level
}

// inBackground runs fn past the request: pi answers some commands only once
// they end. A quick failure still reaches the caller.
func inBackground(ctx context.Context, fn func() error) error {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	return awaitQuickFail(ctx, done)
}

// settleIdle shows the session idle unless a run or a dialog is under way:
// commands that run no agent turn get no agent_settled.
func (d *discoverer) settleIdle(p *sessionProc) {
	p.mu.Lock()
	settled := !p.streaming && len(p.pending) == 0
	p.mu.Unlock()
	if settled {
		d.upsert(p, session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle}, true)
	}
}

// modelUpdate reports a pi model as the card's live model, or nil before one
// is known. A model without reasoning takes no effort, whatever level pi keeps.
func modelUpdate(model string, reasoning bool, level string) *registry.ModelUpdate {
	if model == "" {
		return nil
	}
	effort := ""
	if reasoning {
		effort = level
	}
	return &registry.ModelUpdate{Name: model, Effort: &effort}
}

func (d *discoverer) sendPrompt(ctx context.Context, id, text string) error {
	if p := d.get(id); p != nil {
		return d.sendPromptOwned(ctx, p, text)
	}
	if fs := d.lookupForeign(id); fs != nil {
		if name, args := slashCommand(text); name == "/compact" {
			return d.beaconSend(fs, map[string]any{"type": "intent_compact", "instructions": args})
		}
		fs.mu.Lock()
		busy := fs.streaming
		fs.mu.Unlock()
		return d.beaconSend(fs, map[string]any{
			"type": "intent_prompt", "text": text, "busy": busy,
		})
	}
	return fmt.Errorf("pi: unknown session %s", id)
}

// slashCommand splits a prompt into its first word and the rest.
func slashCommand(text string) (name, args string) {
	name = strings.TrimSpace(text)
	if i := strings.IndexFunc(name, unicode.IsSpace); i >= 0 {
		name, args = name[:i], strings.TrimSpace(name[i:])
	}
	return name, args
}

func isNewCommand(text string) bool {
	name, args := slashCommand(text)
	return name == "/new" && args == ""
}

// quickFail bounds how long a request waits for pi to reject a command; one
// still running past it finishes in the background.
var quickFail = time.Second

func awaitQuickFail(ctx context.Context, done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-time.After(quickFail):
		return nil
	case <-ctx.Done():
		return nil // the command keeps running
	}
}

// runShell runs "!cmd" / "!!cmd" in a session. An owned session shows working
// until the command ends.
// It reports whether the command waits in the session's pane for Enter.
func (d *discoverer) runShell(ctx context.Context, id, text string) (bool, error) {
	if isNewCommand(text) {
		return d.runNew(ctx, id)
	}
	exclude := strings.HasPrefix(text, "!!")
	command := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, "!"), "!"))
	if command == "" {
		return false, errors.New("pi: empty shell command")
	}
	p := d.get(id)
	if p == nil {
		if fs := d.lookupForeign(id); fs != nil {
			err := d.stageForeign(ctx, fs, strings.TrimSpace(text), api.ErrShellNeedsPane)
			return err == nil, err
		}
		return false, fmt.Errorf("pi: unknown session %s", id)
	}
	d.upsert(p, session.StatusWorking, nil, false)
	return false, d.runCommand(ctx, p, "bash", map[string]any{"command": command, "excludeFromContext": exclude})
}

// runNew starts a new session in place of id. A foreign session runs /new only
// from pi's editor, so it waits there for Enter in the session's pane.
func (d *discoverer) runNew(ctx context.Context, id string) (bool, error) {
	if p := d.get(id); p != nil {
		return false, d.newSession(ctx, p)
	}
	if fs := d.lookupForeign(id); fs != nil {
		err := d.stageForeign(ctx, fs, "/new", errNewNeedsPane)
		return err == nil, err
	}
	return false, fmt.Errorf("pi: unknown session %s", id)
}

var errNewNeedsPane = errors.New("/new needs a tmux session")

// queueFor returns the prompt queue of the session whose file is path. A
// foreign session's queue holds only the prompts Argus sent.
func (d *discoverer) queueFor(path string) []string {
	if path == "" {
		return nil
	}
	d.mu.Lock()
	cards := make([]*card, 0, len(d.procs)+len(d.foreign))
	for _, p := range d.procs {
		cards = append(cards, &p.card)
	}
	for _, fs := range d.foreign {
		cards = append(cards, &fs.card)
	}
	d.mu.Unlock()
	for _, c := range cards {
		c.mu.Lock()
		file, q := c.file, c.queue
		c.mu.Unlock()
		if file == path {
			return q
		}
	}
	return nil
}

// rename sets the pi session name: over RPC for owned sessions, as an intent
// for foreign ones. The name is held at once so the next card push keeps it.
func (d *discoverer) rename(ctx context.Context, id, name string) error {
	if p := d.get(id); p != nil {
		if _, err := p.conn.call(ctx, d.nextID(), "set_session_name", map[string]any{"name": name}); err != nil {
			return err
		}
		p.mu.Lock()
		p.name = name
		p.mu.Unlock()
		return nil
	}
	if fs := d.lookupForeign(id); fs != nil {
		if err := d.beaconSend(fs, map[string]any{"type": "intent_rename", "name": name}); err != nil {
			return err
		}
		fs.mu.Lock()
		fs.name = name
		fs.mu.Unlock()
		return nil
	}
	return fmt.Errorf("pi: unknown session %s", id)
}

func (d *discoverer) sendPromptOwned(ctx context.Context, p *sessionProc, text string) error {
	if name, args := slashCommand(text); name == "/compact" {
		return d.compact(ctx, p, args)
	}
	p.mu.Lock()
	busy := p.streaming
	p.mu.Unlock()
	params := map[string]any{"message": text}
	if busy {
		// A prompt without streamingBehavior errors while the agent runs;
		// steer delivers after the current turn's tool calls.
		params["streamingBehavior"] = "steer"
	}
	// pi answers a slash command only once it ends, dialogs included.
	return inBackground(ctx, func() error {
		data, err := p.conn.call(context.Background(), d.nextID(), "prompt", params)
		if err == nil {
			d.afterPrompt(p, data)
		}
		return err
	})
}

// compact runs pi's /compact, which pi keeps out of RPC prompts.
func (d *discoverer) compact(ctx context.Context, p *sessionProc, instructions string) error {
	params := map[string]any{}
	if instructions != "" {
		params["customInstructions"] = instructions
	}
	return d.runCommand(ctx, p, "compact", params)
}

// runCommand runs an RPC command that no agent turn follows.
func (d *discoverer) runCommand(ctx context.Context, p *sessionProc, method string, params map[string]any) error {
	return inBackground(ctx, func() error {
		_, err := p.conn.call(context.Background(), d.nextID(), method, params)
		d.settleIdle(p)
		return err
	})
}

// newSession runs pi's /new, which pi keeps out of RPC prompts. The process
// moves to a new session id; loadState follows it.
func (d *discoverer) newSession(ctx context.Context, p *sessionProc) error {
	return inBackground(ctx, func() error {
		defer d.settleIdle(p)
		data, err := p.conn.call(context.Background(), d.nextID(), "new_session", nil)
		if err != nil {
			return err
		}
		var r struct {
			Cancelled bool `json:"cancelled"`
		}
		_ = json.Unmarshal(data, &r)
		if r.Cancelled {
			return errors.New("pi: an extension cancelled the new session")
		}
		_, err = d.loadState(context.Background(), p)
		return err
	})
}

func (d *discoverer) afterPrompt(p *sessionProc, data json.RawMessage) {
	var r struct {
		Disposition string `json:"disposition"`
	}
	_ = json.Unmarshal(data, &r)
	if r.Disposition != "handled" {
		d.upsert(p, session.StatusWorking, nil, false)
		return
	}
	// A command or input handler took it: no run follows to settle the card.
	d.settleIdle(p)
}

func (d *discoverer) dismiss(ctx context.Context, id string) error {
	d.mu.Lock()
	p := d.procs[id]
	delete(d.procs, id)
	d.mu.Unlock()
	if p == nil {
		// Foreign sessions untrack only: the user's own process keeps running.
		// Unknown ids still emit Dead so a lost card is always cleared.
		if !d.forgetForeign(id, nil) {
			d.markDead(id)
		}
		return nil
	}
	// The relay closes pi's stdin, which asks pi to dispose the runtime and
	// exit, and kills pi if it has not exited in time.
	_ = p.conn.stop()
	ctx, cancel := context.WithTimeout(ctx, dismissKillAfter+time.Second)
	defer cancel()
	select {
	case <-p.conn.done:
	case <-ctx.Done():
	}
	_ = p.conn.close()
	d.markProcDead(p)
	return nil
}

// forget drops a session whose relay connection ended on its own.
func (d *discoverer) forget(p *sessionProc) {
	d.mu.Lock()
	delete(d.procs, p.sessionID())
	d.mu.Unlock()
	d.markProcDead(p)
}

func (d *discoverer) markDead(id string) {
	d.reg.ApplyHook(registry.HookUpdate{Agent: Agent, AgentSessionID: id, Status: session.StatusDead})
}

// markProcDead ends p's card, which may still be indexed under the session p
// left if no push has carried the switch yet.
func (d *discoverer) markProcDead(p *sessionProc) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d.reg.ApplyHook(registry.HookUpdate{Agent: Agent, AgentSessionID: p.id, PrevAgentSessionID: p.prevID, Status: session.StatusDead})
}

func (d *discoverer) upsert(p *sessionProc, st session.Status, ix *session.Interaction, replace bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d.reg.ApplyHook(p.hookUpdate(st, ix, replace))
	p.prevID = ""
}

func (d *discoverer) procEvent(p *sessionProc, typ string, raw json.RawMessage) {
	p.mu.Lock()
	hasPending := len(p.pending) > 0
	p.mu.Unlock()
	switch typ {
	case "agent_start", "turn_start", "message_start", "tool_execution_start":
		p.mu.Lock()
		p.streaming = true
		p.mu.Unlock()
		fallthrough
	// agent_end ends one low-level run; retries/compaction/queued work may
	// follow, and agent_settled is the authoritative idle signal. A manual
	// /compact aborts the run first, so its agent_settled comes before
	// compaction_start; the compact reply settles the card.
	case "compaction_start", "agent_end":
		// A trailing activity event after a dialog must not clear the pending
		// prompt (same hazard as opencode's post-permission activity).
		if hasPending {
			return
		}
		d.upsert(p, session.StatusWorking, nil, false)
	case "agent_settled":
		p.mu.Lock()
		p.streaming = false
		// pi emits nothing for a dialog its caller aborts, but a run cannot
		// settle while one of its dialogs is open.
		maps.DeleteFunc(p.pending, func(_ string, u *pendingUI) bool { return u.inRun })
		hasPending = len(p.pending) > 0
		p.mu.Unlock()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if changed, err := d.loadState(ctx, p); err == nil && changed {
				d.upsert(p, "", nil, false)
			}
		}()
		if hasPending {
			return
		}
		d.upsert(p, session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle}, true)
	case "thinking_level_changed":
		var ev struct {
			Level string `json:"level"`
		}
		if json.Unmarshal(raw, &ev) != nil {
			return
		}
		p.mu.Lock()
		p.level = ev.Level
		p.mu.Unlock()
		d.upsert(p, "", nil, false)
	case "session_info_changed":
		var ev struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &ev) == nil && ev.Name != "" {
			p.mu.Lock()
			p.name = ev.Name
			p.mu.Unlock()
			d.upsert(p, "", nil, false)
		}
	case "extension_ui_request":
		d.applyUIRequest(p, raw)
	case "queue_update":
		var ev struct {
			Steering []string `json:"steering"`
			FollowUp []string `json:"followUp"`
		}
		if json.Unmarshal(raw, &ev) != nil {
			return
		}
		p.mu.Lock()
		p.queue = append(ev.Steering, ev.FollowUp...)
		p.mu.Unlock()
	}
}

// uiRequest is the wire shape of an extension dialog in RPC mode.
type uiRequest struct {
	ID      string   `json:"id"`
	Method  string   `json:"method"`
	Title   string   `json:"title"`
	Message string   `json:"message"`
	Options []string `json:"options"`
	Timeout int      `json:"timeout"` // ms; pi answers the dialog itself once it passes
}

// uiInteraction interprets one extension dialog, shared by owned RPC sessions
// (extension_ui_request) and foreign beacons (ui_request). It returns the
// parked dialog and its Argus interaction.
func uiInteraction(req uiRequest) (*pendingUI, *session.Interaction, bool) {
	pend := &pendingUI{uiID: req.ID, method: req.Method, title: req.Title, message: req.Message, options: req.Options}
	var ix *session.Interaction
	switch req.Method {
	case "confirm":
		msg := req.Title
		if req.Title != "" && req.Message != "" {
			msg += "\n\n"
		}
		ix = &session.Interaction{
			Kind:    session.InteractionPermission,
			Message: msg + req.Message,
			Options: []session.DecisionOption{
				{Label: "Allow", Value: "allow"},
				{Label: "Deny", Value: "deny", Reject: true},
			},
		}
	case "select", "input", "editor":
		ix = &session.Interaction{
			Kind:    session.InteractionQuestion,
			Message: req.Message,
			Questions: []session.QuestionSpec{{
				Header:   req.Title,
				Question: cmp.Or(req.Title, req.Message),
				Options:  req.Options,
			}},
			// An empty answer is a valid value, unlike a cancel.
			AllowUnanswered: req.Method != "select",
		}
	default:
		return nil, nil, false // notify, setStatus, setWidget, setTitle, set_editor_text
	}
	ix.RequestID = req.ID
	return pend, ix, true
}

// applyUIRequest surfaces an extension dialog as an Argus interaction and
// parks it for Respond. Dialog methods (select/confirm/input/editor) block the
// extension until extension_ui_response; fire-and-forget methods (notify,
// setStatus, ...) carry no decision and are ignored here.
func (d *discoverer) applyUIRequest(p *sessionProc, raw json.RawMessage) {
	var req uiRequest
	if json.Unmarshal(raw, &req) != nil || req.ID == "" {
		return
	}
	pend, ix, ok := uiInteraction(req)
	if !ok {
		return
	}
	p.mu.Lock()
	pend.inRun = p.streaming
	p.pending[pend.uiID] = pend
	p.mu.Unlock()
	d.upsert(p, session.StatusAwaitingInput, ix, true)
	if req.Timeout > 0 {
		time.AfterFunc(time.Duration(req.Timeout)*time.Millisecond, func() { d.dropOwnedPending(p, pend.uiID) })
	}
}

// dropOwnedPending removes a dialog that is no longer waiting and, once none
// is left, takes the prompt down from the card.
func (d *discoverer) dropOwnedPending(p *sessionProc, uiID string) {
	if p.dropPending(uiID) {
		d.upsert(p, session.StatusWorking, nil, false)
	}
}
