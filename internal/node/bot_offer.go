package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/bot/memory"
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

// offerCap is how many offers the dial allows a bot per day.
func offerCap(set bot.Settings) int {
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

// offersSince counts the bot's offers made at or after t.
func (d *Node) offersSince(ctx context.Context, botID string, t time.Time) int {
	threads, err := d.bots.ListThreads(ctx, botID)
	if err != nil {
		return 0
	}
	n := 0
	for _, th := range threads {
		if th.Kind != threadChat {
			continue
		}
		evs, err := d.bots.Events(ctx, th.ID, 0)
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
	a, th, err := d.botCaller(ctx, caller)
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
	set := d.botSettings(a)
	limit := offerCap(set)
	if limit == 0 {
		return nil, fmt.Errorf("offers are off for this bot")
	}
	now := d.botNow().In(botLoc(set))
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if d.offersSince(ctx, a.ID, day) >= limit {
		return nil, fmt.Errorf("already made %d offers today (the limit); try again tomorrow", limit)
	}
	target := th
	if th.Kind != threadChat {
		latest, ok, err := d.bots.LatestThread(ctx, a.ID, threadChat)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("the user has no chat with you yet")
		}
		target = latest
	}
	offer := offerPayload{OfferID: "of_" + strings.TrimPrefix(bot.NewID(), "bot_"), Title: title, Details: strings.TrimSpace(p.Details), Prompt: prompt}
	if err := d.botEvent(ctx, target.ID, evOffer, th.StartedBy, offer, "", 0); err != nil {
		return nil, err
	}
	d.botNotify(ctx, a, target.ID, NotifyOffer, th.StartedBy, a.Name+" offers: "+title, offer.Details)
	return map[string]string{"status": "offered; the user decides whether to start it", "offer_id": offer.OfferID}, nil
}

// findOffer returns the offer in the thread and whether it was already
// started or dismissed.
func (d *Node) findOffer(ctx context.Context, p api.BotOfferParams) (bot.Thread, offerPayload, string, error) {
	var offer offerPayload
	th, ok, err := d.bots.Thread(ctx, p.ThreadID)
	if err != nil {
		return th, offer, "", botErr("%v", err)
	}
	if !ok {
		return th, offer, "", botErr("thread not found")
	}
	evs, err := d.bots.Events(ctx, th.ID, 0)
	if err != nil {
		return th, offer, "", botErr("%v", err)
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
		return th, offer, "", botErr("offer not found")
	}
	return th, offer, decided, nil
}

func (d *Node) handleBotOfferStart(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotOfferParams](params)
	if err != nil {
		return nil, err
	}
	if _, err := d.botStore(); err != nil {
		return nil, err
	}
	th, offer, decided, err := d.findOffer(ctx, p)
	if err != nil {
		return nil, err
	}
	if decided != "" {
		return nil, botErr("offer was already %s", decided)
	}
	raw, err := json.Marshal(api.BotSendParams{BotID: th.BotID, ThreadID: th.ID, Text: offer.Prompt})
	if err != nil {
		return nil, err
	}
	res, err := d.handleBotThreadSend(ctx, raw)
	if err != nil {
		return nil, err
	}
	d.logEvent(ctx, th.ID, evOfferStarted, map[string]string{"offer_id": offer.OfferID, "title": offer.Title}, "")
	return res, nil
}

func (d *Node) handleBotOfferDismiss(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotOfferParams](params)
	if err != nil {
		return nil, err
	}
	if _, err := d.botStore(); err != nil {
		return nil, err
	}
	th, offer, decided, err := d.findOffer(ctx, p)
	if err != nil {
		return nil, err
	}
	if decided != "" {
		return nil, botErr("offer was already %s", decided)
	}
	d.logEvent(ctx, th.ID, evOfferDismissed, map[string]string{"offer_id": offer.OfferID, "title": offer.Title}, "")
	return struct{}{}, nil
}
