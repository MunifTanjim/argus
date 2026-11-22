package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/memory"
)

const (
	evOffer          = "offer"
	evOfferStarted   = "offer.started"
	evOfferDismissed = "offer.dismissed"
)

type offerParams struct {
	Title   string `json:"title"`
	Details string `json:"details"`
	Prompt  string `json:"prompt"`
}

// offerPayload is the payload of an offer event.
type offerPayload struct {
	OfferID string `json:"offer_id"`
	Title   string `json:"title"`
	Details string `json:"details,omitempty"`
	Prompt  string `json:"prompt"`
}

// offerCap is how many offers the dial allows an agent per day.
func offerCap(set pagent.Settings) int {
	switch set.Proactivity {
	case proactivityOff:
		return 0
	case proactivityLow:
		return 1
	case proactivityHigh:
		return 6
	}
	return 3
}

// offersSince counts the agent's offers made at or after t.
func (d *Node) offersSince(ctx context.Context, agentID string, t time.Time) int {
	threads, err := d.pagents.ListThreads(ctx, agentID)
	if err != nil {
		return 0
	}
	n := 0
	for _, th := range threads {
		if th.Kind != threadChat {
			continue
		}
		evs, err := d.pagents.Events(ctx, th.ID, 0)
		if err != nil {
			continue
		}
		for _, ev := range evs {
			if ev.Kind == evOffer && !ev.CreatedAt.Before(t) {
				n++
			}
		}
	}
	return n
}

func (d *Node) mcpOffer(ctx context.Context, caller callerRef, p offerParams) (any, error) {
	a, th, err := d.pagentCaller(ctx, caller)
	if err != nil {
		return nil, err
	}
	switch {
	case th.Kind == threadChat:
	case th.Kind == threadRun && th.StartedBy == startedByCheckBack:
	default:
		return nil, fmt.Errorf("offer works only in chats and check-back runs")
	}
	title, prompt := strings.TrimSpace(p.Title), strings.TrimSpace(p.Prompt)
	if title == "" || prompt == "" {
		return nil, fmt.Errorf("title and prompt are required")
	}
	for _, f := range []string{p.Title, p.Details, p.Prompt} {
		if rule, bad := memory.LooksSecret(f); bad {
			return nil, fmt.Errorf("looks like a secret (%s); not offered", rule)
		}
	}
	set := d.pagentSettings(a)
	limit := offerCap(set)
	if limit == 0 {
		return nil, fmt.Errorf("offers are off for this agent")
	}
	now := d.pagentNow().In(agentLoc(set))
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if d.offersSince(ctx, a.ID, day) >= limit {
		return nil, fmt.Errorf("already made %d offers today (the limit); try again tomorrow", limit)
	}
	target := th
	if th.Kind != threadChat {
		latest, ok, err := d.pagents.LatestThread(ctx, a.ID, threadChat)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("the user has no chat with you yet")
		}
		target = latest
	}
	offer := offerPayload{OfferID: "of_" + strings.TrimPrefix(pagent.NewID(), "ag_"), Title: title, Details: strings.TrimSpace(p.Details), Prompt: prompt}
	if err := d.pagentEvent(ctx, target.ID, evOffer, th.StartedBy, offer, "", 0); err != nil {
		return nil, err
	}
	d.pagentNotify(ctx, a, target.ID, NotifyOffer, th.StartedBy, a.Name+" offers: "+title, offer.Details)
	return map[string]string{"status": "offered; the user decides whether to start it", "offer_id": offer.OfferID}, nil
}

// findOffer returns the offer in the thread and whether it was already
// started or dismissed.
func (d *Node) findOffer(ctx context.Context, p api.PAgentOfferParams) (pagent.Thread, offerPayload, string, error) {
	var offer offerPayload
	th, ok, err := d.pagents.Thread(ctx, p.ThreadID)
	if err != nil {
		return th, offer, "", pagentErr("%v", err)
	}
	if !ok {
		return th, offer, "", pagentErr("thread not found")
	}
	evs, err := d.pagents.Events(ctx, th.ID, 0)
	if err != nil {
		return th, offer, "", pagentErr("%v", err)
	}
	found, decided := false, ""
	for _, ev := range evs {
		var o offerPayload
		if json.Unmarshal(ev.Payload, &o) != nil || o.OfferID != p.OfferID || p.OfferID == "" {
			continue
		}
		switch ev.Kind {
		case evOffer:
			offer, found = o, true
		case evOfferStarted:
			decided = "started"
		case evOfferDismissed:
			decided = "dismissed"
		}
	}
	if !found {
		return th, offer, "", pagentErr("offer not found")
	}
	return th, offer, decided, nil
}

func (d *Node) handlePAgentOfferStart(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentOfferParams](params)
	if err != nil {
		return nil, err
	}
	if _, err := d.pagentStore(); err != nil {
		return nil, err
	}
	th, offer, decided, err := d.findOffer(ctx, p)
	if err != nil {
		return nil, err
	}
	if decided != "" {
		return nil, pagentErr("offer was already %s", decided)
	}
	raw, err := json.Marshal(api.PAgentSendParams{AgentID: th.AgentID, ThreadID: th.ID, Text: offer.Prompt})
	if err != nil {
		return nil, err
	}
	res, err := d.handlePAgentThreadSend(ctx, raw)
	if err != nil {
		return nil, err
	}
	d.logEvent(ctx, th.ID, evOfferStarted, map[string]string{"offer_id": offer.OfferID, "title": offer.Title}, "")
	return res, nil
}

func (d *Node) handlePAgentOfferDismiss(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentOfferParams](params)
	if err != nil {
		return nil, err
	}
	if _, err := d.pagentStore(); err != nil {
		return nil, err
	}
	th, offer, decided, err := d.findOffer(ctx, p)
	if err != nil {
		return nil, err
	}
	if decided != "" {
		return nil, pagentErr("offer was already %s", decided)
	}
	d.logEvent(ctx, th.ID, evOfferDismissed, map[string]string{"offer_id": offer.OfferID, "title": offer.Title}, "")
	return struct{}{}, nil
}
