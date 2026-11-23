package node

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/push"
)

// Notification kinds, the rows of the push policy.
const (
	// NotifyNeedsYou: a run result needs_you or blocked, an approval card, a
	// hand-off request or a health notice.
	NotifyNeedsYou = "needs_you"
	// NotifyResult: a run result progress or done.
	NotifyResult = "result"
	NotifyBrief  = "brief"
	NotifyOffer  = "offer"
	// NotifyQuiet: no_change results and memory changes; never pushed.
	NotifyQuiet = "quiet"

	notifyAll      = "all"
	notifyNeedsYou = "needs-you"
	notifyOff      = "off"

	// healthRepush is how often an unresolved health notice pushes again.
	healthRepush = 24 * time.Hour
	// notifyDelayDefault applies when no notify-delay is configured.
	notifyDelayDefault = 3 * time.Minute
	quietSummaryLines  = 5
)

// notifyState holds held and delayed bot pushes.
type notifyState struct {
	mu     sync.Mutex
	held   map[string][]botPush // bot id -> pushes held for quiet hours
	health map[string]time.Time // bot id + key -> last push
	notes  map[string]string    // bot id + key -> open health notice

	spawnFails map[string]int // bot id -> spawn or resume failures in a row
}

// botPush is one bot notification.
type botPush struct {
	bot       bot.Bot
	threadID  string
	kind      string
	startedBy string
	title     string
	body      string
	at        time.Time
}

// shouldPush is the push policy table.
func shouldPush(notify, kind string) bool {
	switch notify {
	case notifyOff:
		return false
	case notifyAll:
		return kind != NotifyQuiet
	default: // needs-you
		return kind == NotifyNeedsYou
	}
}

// resultNotifyKind maps a run result to its notification kind.
func resultNotifyKind(result string) string {
	switch result {
	case resultNeedsYou, resultBlocked:
		return NotifyNeedsYou
	case resultProgress, resultDone:
		return NotifyResult
	}
	return NotifyQuiet
}

// inQuietHours reports whether now falls in spec ("HH:MM-HH:MM") in loc.
func inQuietHours(spec string, now time.Time, loc *time.Location) bool {
	if spec == "" {
		return false
	}
	start, end, err := config.ParseQuietHours(spec)
	if err != nil {
		return false
	}
	t := now.In(loc)
	m := t.Hour()*60 + t.Minute()
	if start < end {
		return m >= start && m < end
	}
	return m >= start || m < end
}

func botLoc(set bot.Settings) *time.Location {
	if set.Timezone != "" && set.Timezone != "auto" {
		if loc, err := time.LoadLocation(set.Timezone); err == nil {
			return loc
		}
	}
	return time.Local
}

// botNotify decides whether a bot event becomes a push, per the
// bot's notify setting, quiet hours and presence, and sends it.
//
//   - kind is one of NotifyNeedsYou, NotifyResult, NotifyBrief, NotifyOffer,
//     NotifyQuiet.
//   - threadID is the thread the push opens (sent as bot_thread_id).
//   - startedBy is who started the work ("you" for a chat the user started);
//     only pushes from work the user did not start are held in quiet hours.
//
// It never blocks: delayed pushes run on timers.
func (d *Node) botNotify(ctx context.Context, a bot.Bot, threadID, kind, startedBy, title, body string) {
	set := d.botSettings(a)
	if !shouldPush(set.Notify, kind) {
		return
	}
	p := botPush{bot: a, threadID: threadID, kind: kind, startedBy: startedBy, title: title, body: body, at: d.botNow()}
	if startedBy != startedByYou && inQuietHours(set.QuietHours, p.at, botLoc(set)) {
		d.notify.mu.Lock()
		if d.notify.held == nil {
			d.notify.held = map[string][]botPush{}
		}
		d.notify.held[a.ID] = append(d.notify.held[a.ID], p)
		d.notify.mu.Unlock()
		return
	}
	if d.clientAttached(p.at) {
		delay := set.NotifyDelay
		if delay <= 0 {
			delay = notifyDelayDefault
		}
		time.AfterFunc(delay, func() {
			if d.threadReadSince(context.WithoutCancel(ctx), threadID, p.at) {
				return
			}
			d.sendBotPush(context.WithoutCancel(ctx), p)
		})
		return
	}
	d.sendBotPush(ctx, p)
}

// botHealth posts a health notice push, deduped by key: it pushes once,
// then at most every healthRepush until botHealthResolved clears the key.
func (d *Node) botHealth(ctx context.Context, a bot.Bot, threadID, key, title, body string) {
	k := a.ID + "/" + key
	now := d.botNow()
	d.notify.mu.Lock()
	if d.notify.health == nil {
		d.notify.health = map[string]time.Time{}
	}
	if d.notify.notes == nil {
		d.notify.notes = map[string]string{}
	}
	d.notify.notes[k] = strings.TrimSpace(strings.TrimSpace(title + ": " + body))
	last, seen := d.notify.health[k]
	if seen && now.Sub(last) < healthRepush {
		d.notify.mu.Unlock()
		return
	}
	d.notify.health[k] = now
	d.notify.mu.Unlock()
	d.botNotify(ctx, a, threadID, NotifyNeedsYou, "", title, body)
}

// botHealthResolved clears a health notice key, so it pushes again if it recurs.
func (d *Node) botHealthResolved(a bot.Bot, key string) {
	d.notify.mu.Lock()
	delete(d.notify.health, a.ID+"/"+key)
	delete(d.notify.notes, a.ID+"/"+key)
	d.notify.mu.Unlock()
}

// healthNotes lists the bot's open health notices, oldest key first.
func (d *Node) healthNotes(a bot.Bot) []string {
	d.notify.mu.Lock()
	defer d.notify.mu.Unlock()
	var keys []string
	for k := range d.notify.notes {
		if strings.HasPrefix(k, a.ID+"/") {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = strings.TrimSuffix(d.notify.notes[k], ":")
	}
	return out
}

// threadReadSince reports whether the thread was read at or after t.
func (d *Node) threadReadSince(ctx context.Context, threadID string, t time.Time) bool {
	if threadID == "" || d.bots == nil {
		return false
	}
	th, ok, err := d.bots.Thread(ctx, threadID)
	return err == nil && ok && th.ReadPosition >= t.UnixNano()
}

func (d *Node) sendBotPush(ctx context.Context, p botPush) {
	sinks := d.ownerSinks.Load()
	if sinks == nil {
		return
	}
	// node_id qualifies the ids for clients that see several nodes.
	data := map[string]string{"bot_id": p.bot.ID, "bot_kind": p.kind, "node_id": d.id}
	if p.threadID != "" {
		data["bot_thread_id"] = p.threadID
	}
	title := p.title
	if title == "" {
		title = p.bot.Name
	}
	body := []rune(p.body)
	if len(body) > ownerNoticeBodyRunes {
		body = body[:ownerNoticeBodyRunes]
	}
	n := push.Notification{Title: title, Body: string(body), Data: data}
	ctx = context.WithoutCancel(ctx)
	go func() {
		for _, sink := range *sinks {
			sink.Notify(ctx, n)
		}
	}()
}

// flushQuietHolds sends one summary push per bot whose quiet hours ended
// with pushes held.
func (d *Node) flushQuietHolds(ctx context.Context, now time.Time) {
	d.notify.mu.Lock()
	var ready [][]botPush
	for id, held := range d.notify.held {
		if len(held) == 0 {
			delete(d.notify.held, id)
			continue
		}
		set := d.botSettings(held[0].bot)
		if inQuietHours(set.QuietHours, now, botLoc(set)) {
			continue
		}
		ready = append(ready, held)
		delete(d.notify.held, id)
	}
	d.notify.mu.Unlock()
	for _, held := range ready {
		a := held[0].bot
		lines := make([]string, 0, quietSummaryLines+1)
		for i, p := range held {
			if i == quietSummaryLines {
				lines = append(lines, fmt.Sprintf("and %d more", len(held)-i))
				break
			}
			s := p.title
			if p.body != "" {
				s += ": " + p.body
			}
			lines = append(lines, s)
		}
		title := fmt.Sprintf("%s: %d updates while quiet", a.Name, len(held))
		if len(held) == 1 {
			title = fmt.Sprintf("%s: 1 update while quiet", a.Name)
		}
		d.sendBotPush(ctx, botPush{
			bot: a, threadID: held[len(held)-1].threadID, kind: held[0].kind,
			title: title, body: strings.Join(lines, "\n"), at: now,
		})
	}
}
