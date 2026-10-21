package opencode

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

var _ adapter.PromptCompleter = (*ocAdapter)(nil)

func (a *ocAdapter) CompletePrompt(ctx context.Context, sess session.Session, trigger string) ([]api.PromptCompletion, error) {
	if trigger != "/" && trigger != "@" {
		return nil, nil
	}
	if a.disc == nil {
		return nil, fmt.Errorf("opencode: no active discoverer")
	}
	c, err := a.disc.liveClient()
	if err != nil {
		return nil, err
	}
	if trigger == "@" {
		skills, err := c.listSkills(ctx, sess.Cwd)
		if err != nil {
			return nil, err
		}
		items := make([]api.PromptCompletion, 0, len(skills))
		for _, s := range skills {
			// The OpenCode TUI inserts @<id>, so the id is the completion name.
			if adapter.IsMentionName(s.ID) {
				items = append(items, api.PromptCompletion{Name: s.ID, Description: s.Description})
			}
		}
		return items, nil
	}
	commands, err := c.loadedCommands(ctx, sess.Cwd)
	if err != nil {
		return nil, err
	}
	items := []api.PromptCompletion{adapter.CompactCompletion}
	for _, cmd := range commands {
		if cmd.Name != adapter.CompactCompletion.Name {
			items = append(items, api.PromptCompletion{Name: cmd.Name, Description: cmd.Description})
		}
	}
	return items, nil
}

// loadedCommands retries an empty listing. OpenCode answers with an empty list
// while it loads a directory for the first time, and a loaded list always has
// its built-in commands.
func (c *client) loadedCommands(ctx context.Context, dir string) ([]ocCommand, error) {
	for wait := 200 * time.Millisecond; ; wait *= 2 {
		commands, err := c.listCommands(ctx, dir)
		if err != nil || len(commands) > 0 || wait > time.Second {
			return commands, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// submit runs /compact and listed /commands through their own endpoints and
// attaches each @mention of a listed skill to the prompt. A failed lookup
// still sends the text as a prompt.
func (d *discoverer) submit(ctx context.Context, sess session.Session, text string) error {
	c, err := d.liveClient()
	if err != nil {
		return err
	}
	id := sess.AgentSessionID
	if adapter.IsCompact(text) {
		return c.compact(ctx, id)
	}
	if name, args, ok := slashCommand(text); ok {
		commands, _ := c.loadedCommands(ctx, sess.Cwd)
		for _, cmd := range commands {
			if cmd.Name == name {
				return c.runCommand(ctx, id, name, args)
			}
		}
	}
	words := adapter.Mentions(text, '@')
	if len(words) == 0 {
		return c.sendPrompt(ctx, id, text, nil)
	}
	skills, _ := c.listSkills(ctx, sess.Cwd)
	known := make(map[string]bool, len(skills))
	for _, s := range skills {
		known[s.ID] = true
	}
	var mentions []ocSkillMention
	for _, w := range words {
		if !known[w.Name] {
			continue
		}
		start := utf16Len(text[:w.At])
		mention := text[w.At : w.At+1+len(w.Name)]
		mentions = append(mentions, ocSkillMention{
			ID:      w.Name,
			Mention: ocMention{Start: start, End: start + utf16Len(mention), Text: mention},
		})
	}
	return c.sendPrompt(ctx, id, text, mentions)
}

func slashCommand(text string) (name, args string, ok bool) {
	rest, ok := strings.CutPrefix(text, "/")
	if !ok {
		return "", "", false
	}
	end := strings.IndexFunc(rest, unicode.IsSpace)
	if end < 0 {
		end = len(rest)
	}
	if end == 0 {
		return "", "", false
	}
	return rest[:end], strings.TrimSpace(rest[end:]), true
}

// utf16Len counts s in UTF-16 code units, the offsets OpenCode's JavaScript
// side uses.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}
