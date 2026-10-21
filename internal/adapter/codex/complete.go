package codex

import (
	"context"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

var _ adapter.PromptCompleter = (*cxAdapter)(nil)

type skillMetadata struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	ShortDescription string `json:"shortDescription"`
	Enabled          bool   `json:"enabled"`
	Path             string `json:"path"`
	Interface        *struct {
		ShortDescription string `json:"shortDescription"`
	} `json:"interface"`
}

func (m skillMetadata) summary() string {
	if m.Interface != nil && m.Interface.ShortDescription != "" {
		return m.Interface.ShortDescription
	}
	if m.ShortDescription != "" {
		return m.ShortDescription
	}
	return m.Description
}

func (d *discoverer) listSkills(ctx context.Context, cwd string) ([]skillMetadata, error) {
	c, err := d.liveConn()
	if err != nil {
		return nil, err
	}
	var res struct {
		Data []struct {
			Skills []skillMetadata `json:"skills"`
		} `json:"data"`
	}
	// The app-server caches skills per cwd and would miss a skill added since.
	if err := c.call(ctx, "skills/list", map[string]any{"cwds": []string{cwd}, "forceReload": true}, &res); err != nil {
		return nil, err
	}
	var out []skillMetadata
	for _, entry := range res.Data {
		for _, m := range entry.Skills {
			if m.Enabled {
				out = append(out, m)
			}
		}
	}
	return out, nil
}

func (a *cxAdapter) CompletePrompt(ctx context.Context, sess session.Session, trigger string) ([]api.PromptCompletion, error) {
	switch trigger {
	case "/":
		return []api.PromptCompletion{adapter.CompactCompletion}, nil
	case "$":
		d, err := a.live()
		if err != nil {
			return nil, err
		}
		skills, err := d.listSkills(ctx, sess.Cwd)
		if err != nil {
			return nil, err
		}
		items := make([]api.PromptCompletion, 0, len(skills))
		for _, s := range skills {
			if adapter.IsMentionName(s.Name) {
				items = append(items, api.PromptCompletion{Name: s.Name, Description: s.summary()})
			}
		}
		return items, nil
	}
	return nil, nil
}

// promptInput attaches each distinct known $name mention as a skill item.
func (d *discoverer) promptInput(ctx context.Context, sess session.Session, text string) []map[string]any {
	input := textInput(text)
	mentions := adapter.Mentions(text, '$')
	if len(mentions) == 0 {
		return input
	}
	// A failed lookup still sends the prompt; the mention stays plain text.
	skills, _ := d.listSkills(ctx, sess.Cwd)
	byName := make(map[string]skillMetadata, len(skills))
	for _, s := range skills {
		byName[s.Name] = s
	}
	for _, m := range mentions {
		if s, ok := byName[m.Name]; ok {
			input = append(input, map[string]any{"type": "skill", "name": s.Name, "path": s.Path})
			delete(byName, m.Name)
		}
	}
	return input
}
