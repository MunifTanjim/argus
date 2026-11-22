package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MunifTanjim/argus/internal/agentgit"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/memory"
	"github.com/MunifTanjim/argus/internal/session"
)

const (
	proposalKindInstructions = "instructions"
	sourceAgent              = "agent"
	threadRun                = "run"

	handoffRequestID   = "handoff"
	handoffTakeover    = "takeover"
	handoffDone        = "done"
	handoffDoneMessage = "The user finished: continue."
)

type rememberParams struct {
	Action string `json:"action"`
	Text   string `json:"text"`
	Match  string `json:"match"`
	Until  string `json:"until"`
	Scope  string `json:"scope"`
}

type rememberResult struct {
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

type proposeInstructionsParams struct {
	Instructions string `json:"instructions"`
	Reason       string `json:"reason"`
}

type requestHandoffParams struct {
	Reason string `json:"reason"`
	Steps  string `json:"steps"`
}

// pagentCaller resolves the agent and thread of an MCP caller that is a
// persistent agent's session.
func (d *Node) pagentCaller(ctx context.Context, caller callerRef) (pagent.Agent, pagent.Thread, error) {
	s := caller.Local
	if d.pagents == nil || s == nil || s.PAgentID == "" || s.PAgentThreadID == "" {
		return pagent.Agent{}, pagent.Thread{}, fmt.Errorf("not an agent session")
	}
	a, ok, err := d.pagents.AgentByID(ctx, s.PAgentID)
	if err != nil {
		return pagent.Agent{}, pagent.Thread{}, err
	}
	if !ok {
		return pagent.Agent{}, pagent.Thread{}, fmt.Errorf("not an agent session")
	}
	th, ok, err := d.pagents.Thread(ctx, s.PAgentThreadID)
	if err != nil {
		return pagent.Agent{}, pagent.Thread{}, err
	}
	if !ok {
		return pagent.Agent{}, pagent.Thread{}, fmt.Errorf("not an agent session")
	}
	return a, th, nil
}

func (d *Node) mcpRemember(ctx context.Context, caller callerRef, p rememberParams) (any, error) {
	a, th, err := d.pagentCaller(ctx, caller)
	if err != nil {
		return nil, err
	}
	if th.Kind == threadRun {
		return nil, fmt.Errorf("memory is written only from chats")
	}
	op := memory.Op{Match: p.Match, Text: p.Text, Until: p.Until}
	switch p.Action {
	case "add", "update":
		op.Action = p.Action
	case "forget":
		op.Action = "remove"
	default:
		return nil, fmt.Errorf("action must be add, update or forget")
	}
	switch p.Scope {
	case "", "agent":
	case "profile":
		op.File = profileFile
	default:
		return nil, fmt.Errorf("scope must be agent or profile")
	}
	applied, err := d.pagentApplyMemory(ctx, a, th.ID, sourceAgent, []memory.Op{op}, "")
	if err != nil {
		return nil, err
	}
	if applied > 0 {
		return rememberResult{Status: "saved"}, nil
	}
	return rememberResult{Status: "proposed", Note: "saved for the user to review"}, nil
}

func (d *Node) mcpProposeInstructions(ctx context.Context, caller callerRef, p proposeInstructionsParams) (any, error) {
	a, th, err := d.pagentCaller(ctx, caller)
	if err != nil {
		return nil, err
	}
	for _, f := range []string{p.Instructions, p.Reason} {
		if rule, bad := memory.LooksSecret(f); bad {
			return nil, fmt.Errorf("looks like a secret (%s); not saved", rule)
		}
	}
	body, err := json.Marshal(memoryProposal{
		Kind: proposalKindInstructions, Source: sourceAgent, Text: strings.TrimSpace(p.Instructions), Reason: strings.TrimSpace(p.Reason),
	})
	if err != nil {
		return nil, err
	}
	id := "mp_" + strings.TrimPrefix(pagent.NewID(), "ag_")
	if err := d.pagents.InsertProposal(ctx, pagent.Proposal{ID: id, AgentID: a.ID, ThreadID: th.ID, Ops: body}); err != nil {
		return nil, err
	}
	d.agentMemoryEvent(ctx, a, th.ID, evMemoryProposed, map[string]any{
		"proposal_id": id, "kind": proposalKindInstructions, "reason": strings.TrimSpace(p.Reason),
	})
	d.notifyConns(api.MethodPAgentChanged)
	return rememberResult{Status: "proposed", Note: "the user decides whether to change your instructions"}, nil
}

// applyInstructionsProposal rewrites the agent's instructions with the
// proposal's text and commits it.
func (d *Node) applyInstructionsProposal(ctx context.Context, a pagent.Agent, pr pagent.Proposal, body memoryProposal) (string, error) {
	text := strings.TrimSpace(body.Text)
	if text == "" {
		return "", pagentErr("bad proposal: no instructions")
	}
	if rule, bad := memory.LooksSecret(text); bad {
		return "", pagentErr("looks like a secret (%s); not saved", rule)
	}
	defer lockMemory(a.ID)()
	repo, err := agentgit.Open(ctx, a.Dir, d.pagentGitMode())
	if err != nil {
		return "", pagentErr("%v", err)
	}
	if err := d.commitHandEdits(ctx, repo, a, pr.ThreadID, []string{pagent.DefinitionFile}); err != nil {
		return "", pagentErr("%v", err)
	}
	def, err := pagent.LoadDefinition(a.Dir)
	if err != nil {
		return "", pagentErr("%v", err)
	}
	def.Instructions = text
	if err := def.Save(a.Dir); err != nil {
		return "", pagentErr("%v", err)
	}
	hash, err := repo.CommitPaths(ctx, "agent: update instructions", memoryTrailers(a, sourceAgent, pr.ThreadID), []string{pagent.DefinitionFile}, true)
	if err != nil {
		return "", pagentErr("%v", err)
	}
	return hash, nil
}

func (d *Node) mcpRequestHandoff(ctx context.Context, caller callerRef, p requestHandoffParams) (any, error) {
	if _, _, err := d.pagentCaller(ctx, caller); err != nil {
		return nil, err
	}
	if _, ok := d.reg.Get(caller.Local.ID); !ok {
		return nil, fmt.Errorf("session not found")
	}
	d.reg.PushDecision(caller.Local.ID, session.Interaction{
		Kind:      session.InteractionHandoff,
		Message:   strings.TrimSpace(p.Reason),
		Steps:     strings.TrimSpace(p.Steps),
		RequestID: handoffRequestID,
		Options: []session.DecisionOption{
			{Label: "Take over", Value: handoffTakeover},
			{Label: "Done", Value: handoffDone},
		},
	})
	return map[string]string{"status": "waiting for the user; you'll get a message when they're done"}, nil
}

// respondHandoff answers a pending handoff interaction. done resumes the
// session; takeover is a client action and changes nothing here.
func (d *Node) respondHandoff(ctx context.Context, s session.Session, p api.RespondParams) error {
	ix, ok := d.reg.Decision(s.ID, handoffRequestID)
	if !ok {
		return fmt.Errorf("no pending handoff")
	}
	switch p.OptionValue {
	case handoffTakeover:
		return nil
	case handoffDone:
		if d.reg.OtherPending(s.ID, handoffRequestID) {
			return fmt.Errorf("answer the agent's pending request first")
		}
		d.reg.DropDecision(s.ID, handoffRequestID)
		cur, found := d.reg.Get(s.ID)
		if !found {
			return nil
		}
		if err := d.deliver(ctx, cur, handoffDoneMessage); err != nil {
			d.reg.PushDecision(s.ID, ix)
			return err
		}
		return nil
	}
	return fmt.Errorf("unknown handoff answer %q", p.OptionValue)
}
