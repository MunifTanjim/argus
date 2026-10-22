package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/push"
)

const ownerNoticeBodyRunes = 200

func (d *Node) handleMessagesList(ctx context.Context, _ json.RawMessage) (any, error) {
	if d.mesh == nil {
		return nil, errMeshDisabled
	}
	msgs, err := d.mesh.List(ctx, 500)
	if err != nil {
		return nil, err
	}
	links, err := d.mesh.Links(ctx)
	if err != nil {
		return nil, err
	}
	return api.MessagesListResult{Messages: msgs, Links: links}, nil
}

func (d *Node) handleMessagesSend(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.OwnerSendParams](params)
	if err != nil {
		return nil, err
	}
	if d.mesh == nil {
		return nil, errMeshDisabled
	}
	owner := api.Party{Node: d.id, Key: api.PartyOwner}
	if p.ReplyTo == "" {
		target, ok := d.reg.Get(p.SessionID)
		if !ok {
			return nil, fmt.Errorf("unknown session: %s", p.SessionID)
		}
		return d.sendText(ctx, owner, target, p.SessionID, p.Text)
	}
	orig, err := d.message(ctx, p.ReplyTo)
	if err != nil {
		return nil, err
	}
	if orig.To.Key != api.PartyOwner {
		return nil, errors.New("only messages to the owner can be answered here")
	}
	m, err := d.insertReply(ctx, owner, orig, p.Text, api.MessageQueued)
	if err != nil {
		return nil, err
	}
	return api.AgentSendResult{MessageID: m.ID}, nil
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
	if s, live := d.sessionForKey(m.From.Key); live {
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
