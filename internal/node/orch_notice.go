package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/orch"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
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

func renderInteraction(child session.Session, childID string, i *session.Interaction) string {
	var b strings.Builder
	label := sessionLabel(child)
	switch i.Kind {
	case session.InteractionQuestion:
		fmt.Fprintf(&b, "%s (%s) asks:\n", label, childID)
	case session.InteractionPlan:
		fmt.Fprintf(&b, "%s (%s) asks to approve a plan:\n", label, childID)
	default:
		fmt.Fprintf(&b, "%s (%s) asks for permission:\n", label, childID)
	}
	if i.Message != "" {
		fmt.Fprintf(&b, "%s\n", i.Message)
	}
	switch i.Kind {
	case session.InteractionQuestion:
		for _, q := range i.Questions {
			fmt.Fprintf(&b, "Question: %s\n", q.Question)
			for _, o := range q.Options {
				fmt.Fprintf(&b, "  - %s\n", o)
			}
		}
	case session.InteractionPlan:
		fmt.Fprintf(&b, "%s\n", i.Plan)
	default:
		if i.ToolName != "" {
			fmt.Fprintf(&b, "Tool: %s\n", i.ToolName)
		}
		if i.ToolInput != "" {
			fmt.Fprintf(&b, "Input: %s\n", i.ToolInput)
		}
	}
	if !i.ParentCanAnswer() {
		b.WriteString("The user must answer this in the child's session.\n")
	} else if len(i.Options) > 0 {
		b.WriteString("Options:\n")
		for _, o := range i.Options {
			fmt.Fprintf(&b, "  - %s: %s\n", o.Label, o.Value)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderTurnEnded(child session.Session, childID, taskID, lastOutput string) string {
	return fmt.Sprintf("%s (%s) ended its turn without a report on task %s.\nLast output:\n%s",
		sessionLabel(child), childID, taskID, lastOutput)
}

// orchNotices tells parents about the prompts and turn ends of their children.
func (d *Node) orchNotices(ctx context.Context, snap []session.Session) {
	links, err := d.orch.Links(ctx)
	if err != nil {
		d.log.Warn("orch: list links failed", "err", err)
		return
	}
	byChild := make(map[string]api.Link, len(links))
	for _, l := range links {
		if l.Child.Node == d.id {
			byChild[l.Child.Key] = l
		}
	}
	prev, sigs := d.orchPrev, d.orchSigs
	d.orchPrev = make(map[string]session.Status, len(prev))
	d.orchSigs = make(map[string]string, len(sigs))
	for _, s := range snap {
		d.orchPrev[s.ID] = s.Status
		key := actorKey(s)
		if key == "" {
			continue
		}
		link, ok := byChild[key]
		if !ok || link.Parent.Key == key {
			continue
		}
		d.orchSigs[s.ID] = d.interactionMessages(ctx, s, link, sigs[s.ID])
		if (prev[s.ID] == session.StatusWorking || d.orchWorked[s.ID]) && s.Status != session.StatusWorking && !s.InteractionPending() {
			d.turnEnded(ctx, s, link)
		}
	}
}

// interactionMessages returns the sig to remember; seen stops a re-notice of a prompt
// the parent answered that has not cleared yet.
func (d *Node) interactionMessages(ctx context.Context, s session.Session, link api.Link, seen string) string {
	key := actorKey(s)
	cur := pendingSig(s)
	open, err := d.orch.OpenInteractions(ctx, api.Actor{Node: d.id, Key: key})
	if err != nil {
		d.log.Warn("orch: list interactions failed", "child", key, "err", err)
		return seen
	}
	covered := false
	for _, n := range open {
		if cur != "" && interactionSig(n.Interaction) == cur {
			covered = true
			continue
		}
		d.withdrawInteraction(ctx, n, fmt.Sprintf("The interaction %s of %s (%s) was answered outside argus.",
			n.ID, sessionLabel(s), d.compositeID(s)))
	}
	if cur == "" || covered || cur == seen {
		return cur
	}
	id := orch.NewID(d.id)
	_, err = d.orch.Insert(ctx, api.Message{
		ID: id, Kind: api.MessageInteraction,
		From: api.Actor{Node: d.id, Key: api.ActorArgus}, To: link.Parent, Child: &api.Actor{Node: d.id, Key: key},
		Interaction: s.Interaction, Body: renderInteraction(s, d.compositeID(s), s.Interaction),
		State: api.MessageQueued,
	})
	if err != nil {
		d.log.Warn("orch: insert interaction message failed", "child", key, "err", err)
		return seen
	}
	d.orchChanged()
	return cur
}

// withdrawInteraction tells the parent with ended when it already saw the message.
func (d *Node) withdrawInteraction(ctx context.Context, n api.Message, ended string) {
	ok, err := d.withdrawLocal(ctx, n, ended)
	if err != nil {
		d.log.Warn("orch: withdraw interaction failed", "id", n.ID, "err", err)
	}
	if ok {
		d.orchChanged()
	}
}

// withdrawLocal withdraws the open message m to an actor on this node, and
// gives that actor the interaction_ended message ended when it already saw m. It
// reports whether m was withdrawn.
func (d *Node) withdrawLocal(ctx context.Context, m api.Message, ended string) (bool, error) {
	switch m.State {
	case api.MessageQueued:
		return d.orch.Transition(ctx, m.ID, m.State, api.MessageWithdrawn)
	case api.MessageDelivered:
		// One transaction: a failed insert leaves the message open for a retried withdraw.
		ok, err := d.orch.TransitionAndInsert(ctx, m.ID, m.State, api.MessageWithdrawn, api.Message{
			Kind: api.MessageInteractionEnded, From: api.Actor{Node: d.id, Key: api.ActorArgus}, To: m.To,
			Child: m.Child, Body: ended, State: api.MessageQueued,
		})
		if ok {
			d.orchKick()
		}
		return ok, err
	}
	return false, nil
}

func (d *Node) turnEnded(ctx context.Context, s session.Session, link api.Link) {
	key := actorKey(s)
	task, ok, err := d.orch.UnrepliedTask(ctx, api.Actor{Node: d.id, Key: key})
	if err != nil {
		d.log.Warn("orch: unreplied task failed", "child", key, "err", err)
		return
	}
	if !ok {
		return
	}
	view, err := callWithin(ctx, orchCallTimeout, func(context.Context) (transcript.TranscriptView, error) {
		return d.transcriptView(s)
	})
	if err != nil {
		d.log.Warn("orch: read transcript failed", "child", key, "err", err)
	}
	_, err = d.orch.Insert(ctx, api.Message{
		Kind: api.MessageTurnEnded, From: api.Actor{Node: d.id, Key: api.ActorArgus}, To: link.Parent,
		Child: &api.Actor{Node: d.id, Key: key}, Body: renderTurnEnded(s, d.compositeID(s), task.ID, lastAssistantText(view.Entries)),
		State: api.MessageQueued,
	})
	if err != nil {
		d.log.Warn("orch: insert turn_ended message failed", "child", key, "err", err)
		return
	}
	d.orchChanged()
}
