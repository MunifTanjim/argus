package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/mesh"
	"github.com/MunifTanjim/argus/internal/session"
)

// interactionSig identifies a pending interaction across registry updates.
func interactionSig(i *session.Interaction) string {
	if i == nil {
		return ""
	}
	questions := make([]string, len(i.Questions))
	for n, q := range i.Questions {
		questions[n] = q.Question
	}
	b, _ := json.Marshal(struct {
		Kind      session.InteractionKind
		ToolName  string
		ToolInput string
		Plan      string
		RequestID string
		Questions []string
	}{i.Kind, i.ToolName, i.ToolInput, i.Plan, i.RequestID, questions})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func pendingSig(s session.Session) string {
	if !s.InteractionPending() {
		return ""
	}
	return interactionSig(s.Interaction)
}

func sessionLabel(s session.Session) string {
	if s.Name != "" {
		return s.Name
	}
	return s.Agent
}

func renderPromptNotice(id string, child session.Session, childID string, i *session.Interaction) string {
	var b strings.Builder
	label := sessionLabel(child)
	switch i.Kind {
	case session.InteractionQuestion:
		fmt.Fprintf(&b, "%s (%s) asks:\n", label, childID)
		for _, q := range i.Questions {
			fmt.Fprintf(&b, "Question: %s\n", q.Question)
			for _, o := range q.Options {
				fmt.Fprintf(&b, "  - %s\n", o)
			}
		}
	case session.InteractionPlan:
		fmt.Fprintf(&b, "%s (%s) asks to approve a plan:\n%s\n", label, childID, i.Plan)
		if len(i.Options) > 0 {
			b.WriteString("Options:\n")
			for _, o := range i.Options {
				fmt.Fprintf(&b, "  - %s: %s\n", o.Label, o.Value)
			}
		}
	default:
		fmt.Fprintf(&b, "%s (%s) asks for permission:\n", label, childID)
		if i.ToolName != "" {
			fmt.Fprintf(&b, "Tool: %s\n", i.ToolName)
		}
		if i.ToolInput != "" {
			fmt.Fprintf(&b, "Input: %s\n", i.ToolInput)
		}
	}
	fmt.Fprintf(&b, "Answer: argus session respond %s allow|deny [--reason \"<text>\"]\n", id)
	fmt.Fprintf(&b, "        argus session respond %s --option <value>\n", id)
	fmt.Fprintf(&b, "        argus session respond %s --answer \"<question>=<option label>\"", id)
	return b.String()
}

func renderTurnNotice(child session.Session, childID, taskID, lastOutput string) string {
	return fmt.Sprintf("%s (%s) ended its turn without a report on task %s.\nLast output:\n%s",
		sessionLabel(child), childID, taskID, lastOutput)
}

// meshNotices tells parents about the prompts and turn ends of their children.
func (d *Node) meshNotices(ctx context.Context, snap []session.Session) {
	links, err := d.mesh.Links(ctx)
	if err != nil {
		d.log.Warn("mesh: list links failed", "err", err)
		return
	}
	byChild := make(map[string]api.Link, len(links))
	for _, l := range links {
		byChild[l.Child.Key] = l
	}
	prev, sigs := d.meshPrev, d.meshSigs
	d.meshPrev = make(map[string]session.Status, len(prev))
	d.meshSigs = make(map[string]string, len(sigs))
	for _, s := range snap {
		d.meshPrev[s.ID] = s.Status
		key := partyKey(s)
		link, ok := byChild[key]
		if !ok || link.Parent.Key == key {
			continue
		}
		d.meshSigs[s.ID] = d.promptNotices(ctx, s, link, sigs[s.ID])
		if (prev[s.ID] == session.StatusWorking || d.meshWorked[s.ID]) && s.Status != session.StatusWorking && !s.InteractionPending() {
			d.turnNotice(ctx, s, link)
		}
	}
}

// promptNotices returns the sig to remember; seen stops a re-notice of a prompt
// the parent answered that has not cleared yet.
func (d *Node) promptNotices(ctx context.Context, s session.Session, link api.Link, seen string) string {
	key := partyKey(s)
	cur := pendingSig(s)
	open, err := d.mesh.OpenNotices(ctx, key)
	if err != nil {
		d.log.Warn("mesh: list notices failed", "subject", key, "err", err)
		return seen
	}
	covered := false
	for _, n := range open {
		if cur != "" && interactionSig(n.Interaction) == cur {
			covered = true
			continue
		}
		d.withdrawNotice(ctx, n, s, link)
	}
	if cur == "" || covered || cur == seen {
		return cur
	}
	id := mesh.NewID()
	if _, err := d.mesh.Insert(ctx, api.Message{
		ID: id, Kind: api.MessagePromptNotice,
		From: api.Party{Node: d.id, Key: api.PartyArgus}, To: link.Parent, Subject: key,
		Interaction: s.Interaction, Body: renderPromptNotice(id, s, d.compositeID(s), s.Interaction),
		State: api.MessageQueued,
	}); err != nil {
		d.log.Warn("mesh: insert prompt notice failed", "subject", key, "err", err)
		return seen
	}
	d.meshChanged()
	return cur
}

func (d *Node) withdrawNotice(ctx context.Context, n api.Message, s session.Session, link api.Link) {
	ok, err := d.mesh.Transition(ctx, n.ID, n.State, api.MessageWithdrawn)
	if err != nil {
		d.log.Warn("mesh: withdraw notice failed", "id", n.ID, "err", err)
		return
	}
	if !ok {
		return
	}
	d.meshChanged()
	if n.State != api.MessageDelivered {
		return
	}
	body := fmt.Sprintf("The prompt %s of %s (%s) was answered outside argus.", n.ID, sessionLabel(s), d.compositeID(s))
	if _, err := d.mesh.Insert(ctx, api.Message{
		Kind: api.MessageAnsweredNotice, From: api.Party{Node: d.id, Key: api.PartyArgus}, To: link.Parent,
		Subject: n.Subject, Body: body, State: api.MessageQueued,
	}); err != nil {
		d.log.Warn("mesh: insert answered notice failed", "id", n.ID, "err", err)
	}
}

func (d *Node) turnNotice(ctx context.Context, s session.Session, link api.Link) {
	key := partyKey(s)
	task, ok, err := d.mesh.UnrepliedTask(ctx, key)
	if err != nil {
		d.log.Warn("mesh: unreplied task failed", "child", key, "err", err)
		return
	}
	if !ok {
		return
	}
	view, err := d.transcriptView(s)
	if err != nil {
		d.log.Warn("mesh: read transcript failed", "child", key, "err", err)
	}
	if _, err := d.mesh.Insert(ctx, api.Message{
		Kind: api.MessageTurnNotice, From: api.Party{Node: d.id, Key: api.PartyArgus}, To: link.Parent,
		Subject: key, Body: renderTurnNotice(s, d.compositeID(s), task.ID, lastAssistantText(view.Entries)),
		State: api.MessageQueued,
	}); err != nil {
		d.log.Warn("mesh: insert turn notice failed", "child", key, "err", err)
		return
	}
	d.meshChanged()
}
