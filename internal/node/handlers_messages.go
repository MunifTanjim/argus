package node

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/push"
)

const ownerNoticeBodyRunes = 200

func (d *Node) handleMessageList(ctx context.Context, _ json.RawMessage) (any, error) {
	if d.orch == nil {
		return nil, errOrchDisabled
	}
	msgs, err := d.orch.List(ctx, 500)
	if err != nil {
		return nil, err
	}
	links, err := d.orch.Links(ctx)
	if err != nil {
		return nil, err
	}
	return api.MessagesListResult{Messages: msgs, Links: links}, nil
}

func (d *Node) handleMessageSend(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.OwnerSendParams](params)
	if err != nil {
		return nil, err
	}
	if d.orch == nil {
		return nil, errOrchDisabled
	}
	owner := api.Actor{Node: d.id, Key: api.ActorOwner}
	if p.ReplyTo == "" {
		target, err := d.agentTarget(p.SessionID)
		if err != nil {
			return nil, err
		}
		return d.sendText(ctx, callerRef{Actor: owner}, target, p.SessionID, p.Text, "")
	}
	orig, err := d.mustMessage(ctx, p.ReplyTo)
	if err != nil {
		return nil, err
	}
	if orig.To != owner {
		return nil, errors.New("only messages to the owner can be answered here")
	}
	m, err := d.insertReply(ctx, callerRef{Actor: owner}, orig, p.Text, api.MessageQueued)
	if err != nil {
		return nil, err
	}
	return api.OrchSendResult{MessageID: m.ID}, nil
}

// notifyOwner bypasses push.Watch and its delay. Sinks run in the background so a
// slow push cannot stall or be cancelled with the caller's RPC.
func (d *Node) notifyOwner(ctx context.Context, m api.Message, senderLabel string) {
	sinks := d.ownerSinks.Load()
	if sinks == nil {
		return
	}
	body := []rune(m.Body)
	if len(body) > ownerNoticeBodyRunes {
		body = body[:ownerNoticeBodyRunes]
	}
	data := map[string]string{}
	if s, live := d.sessionForKey(m.From.Key); live && m.From.Node == d.id {
		data["session_id"] = s.ID
	}
	n := push.Notification{Title: senderLabel + " replied", Body: string(body), Data: data}
	ctx = context.WithoutCancel(ctx)
	go func() {
		for _, sink := range *sinks {
			sink.Notify(ctx, n)
		}
	}()
}
