package codex

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func parseRollout(path string) ([]transcript.Chunk, error) {
	lines, err := scanRollout(path)
	if err != nil {
		return nil, err
	}
	return foldRollout(lines, loadModelNames()), nil
}

// foldRollout folds a flat rollout line list into display chunks. Pure in its inputs,
// so streaming can re-fold an accumulating line slice each Refresh.
func foldRollout(lines []rolloutLine, models map[string]string) []transcript.Chunk {
	var out []transcript.Chunk
	var ai *transcript.Chunk // open AI chunk for the current turn
	model := ""
	firstCtxTokens := map[*transcript.Chunk]int{}
	nicknames := map[string]string{} // agent id -> nickname, from each spawn seen so far
	lastUserTurnID := ""             // turn_id of the trailing user chunk, for skill folding

	flush := func() {
		if ai != nil {
			if len(ai.Items) == 0 {
				ai = nil // aborted/usage-only turn: nothing to display
				return
			}
			for _, it := range ai.Items {
				if it.Kind == transcript.ItemTool || it.Kind == transcript.ItemSubagent {
					ai.ToolCount++
				}
			}
			out = append(out, *ai)
			ai = nil
		}
	}
	ensureAI := func(ts string) *transcript.Chunk {
		if ai == nil {
			ai = &transcript.Chunk{Kind: transcript.ChunkAI, Timestamp: ts, ModelName: model, ModelColor: modelColorFor(model)}
		}
		return ai
	}

	for _, l := range lines {
		p := l.Payload
		switch l.Type {
		case "turn_context":
			model = displayModel(p.Model, models)
		case "event_msg":
			switch p.Type {
			case "token_count":
				if p.Info != nil {
					applyTokenCount(ensureAI(l.Timestamp), p.Info, firstCtxTokens)
				}
			case "task_complete":
				if ai != nil && p.DurationMs > 0 {
					ai.DurationMs = p.DurationMs
				}
			}
		case "response_item":
			switch p.Type {
			case "message":
				switch p.Role {
				case "user":
					text := contentText(p.Content)
					if isScaffolding(text) {
						continue
					}
					flush()
					if cmd, result, ok := userShellCommand(text); ok {
						code, _ := exitCodeAfter(result, "Exit code: ")
						out = append(out, transcript.Chunk{
							Kind: transcript.ChunkShell, Timestamp: l.Timestamp,
							Text: cmd, Detail: result, IsError: code != 0,
						})
						continue
					}
					if name, _, body, ok := skillLoad(text); ok {
						input, _ := json.Marshal(map[string]string{"skill": name})
						item := transcript.Item{
							Kind:         transcript.ItemSkill,
							ToolName:     "Skill",
							ToolID:       p.ID,
							ToolInput:    string(input),
							InputPreview: name,
							Result:       body,
						}
						// The skill load is a follow-up to the user's invoking message; fold it in
						// when the trailing chunk is that same-turn user message.
						turn := p.turnID()
						if n := len(out); n > 0 && out[n-1].Kind == transcript.ChunkUser && turnsMatch(lastUserTurnID, turn) {
							out[n-1].Items = append(out[n-1].Items, item)
						} else {
							out = append(out, transcript.Chunk{
								Kind: transcript.ChunkUser, Timestamp: l.Timestamp,
								Items: []transcript.Item{item},
							})
						}
						lastUserTurnID = turn
						continue
					}
					lastUserTurnID = p.turnID()
					out = append(out, transcript.Chunk{Kind: transcript.ChunkUser, Timestamp: l.Timestamp, Text: text})
				case "assistant":
					c := ensureAI(l.Timestamp)
					c.Items = append(c.Items, transcript.Item{Kind: transcript.ItemText, Text: contentText(p.Content)})
				}
			case "reasoning":
				// Show a thinking item even when summary is empty (encrypted reasoning).
				c := ensureAI(l.Timestamp)
				c.Items = append(c.Items, transcript.Item{Kind: transcript.ItemThinking, Text: summaryText(p.Summary)})
				c.Thinking++
			case "function_call":
				c := ensureAI(l.Timestamp)
				it := transcript.Item{
					ToolName:  p.Name,
					ToolID:    p.CallID,
					ToolInput: argString(p.Arguments),
					Kind:      transcript.ItemTool,
				}
				switch p.Name {
				case "spawn_agent":
					it.Kind = transcript.ItemSubagent
					typ, desc := spawnArgs(p.Arguments)
					it.Subagents = []transcript.Subagent{{Type: typ, Desc: desc}}
				case "wait_agent", "close_agent":
					it.Kind = transcript.ItemSubagent
					it.Subagents = buildSubagents(waitCloseTargets(p.Name, p.Arguments), nicknames)
				}
				c.Items = append(c.Items, it)
			case "function_call_output":
				out := outputText(p.Output)
				setResult(ai, p.CallID, out)
				if id, nick := spawnResult(out); id != "" {
					setSpawnResult(ai, p.CallID, id, nick)
					if nick != "" {
						nicknames[id] = nick
					}
				}
			case "custom_tool_call":
				c := ensureAI(l.Timestamp)
				c.Items = append(c.Items, transcript.Item{
					ToolName:  p.Name,
					ToolID:    p.CallID,
					ToolInput: p.Input,
					Kind:      transcript.ItemTool,
				})
			case "custom_tool_call_output":
				setResult(ai, p.CallID, outputText(p.Output))
			case "web_search_call":
				// No paired output event; web_search_end is ignored as a duplicate.
				c := ensureAI(l.Timestamp)
				c.Items = append(c.Items, transcript.Item{
					ToolName:  "web_search",
					ToolID:    p.ID,
					ToolInput: string(p.Action),
					Kind:      transcript.ItemTool,
				})
			}
		}
	}
	flush()
	stampIDs(out)
	return out
}

func isScaffolding(text string) bool {
	t := strings.TrimSpace(text)
	return strings.HasPrefix(t, "<environment_context>") || strings.HasPrefix(t, "<subagent_notification>")
}

func userShellCommand(text string) (cmd, result string, ok bool) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "<user_shell_command>") || !strings.HasSuffix(t, "</user_shell_command>") {
		return "", "", false
	}
	cmd = tagContent(t, "command")
	if cmd == "" {
		return "", "", false
	}
	return cmd, tagContent(t, "result"), true
}

func tagContent(s, tag string) string {
	open, close := "<"+tag+">", "</"+tag+">"
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	i += len(open)
	j := strings.Index(s[i:], close)
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(s[i : i+j])
}

// turnsMatch reports whether two turn ids are compatible for folding a skill load
// into the preceding user chunk: a positive mismatch blocks it, a missing id on
// either side falls back to positional adjacency.
func turnsMatch(a, b string) bool {
	return a == "" || b == "" || a == b
}

func skillLoad(text string) (name, path, body string, ok bool) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "<skill>") || !strings.HasSuffix(t, "</skill>") {
		return "", "", "", false
	}
	name = tagContent(t, "name")
	if name == "" {
		return "", "", "", false
	}
	path = tagContent(t, "path")
	inner := strings.TrimSuffix(t, "</skill>")
	if i := strings.Index(inner, "</path>"); i >= 0 {
		inner = inner[i+len("</path>"):]
	}
	return name, path, stripFrontmatter(strings.TrimSpace(inner)), true
}

func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return s
	}
	rest := s[len("---\n"):]
	i := strings.Index(rest, "\n---")
	if i < 0 {
		return s
	}
	return strings.TrimLeft(rest[i+len("\n---"):], "\n")
}

func contentText(parts []rolloutContent) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

func displayModel(slug string, names map[string]string) string {
	if dn := names[slug]; dn != "" {
		return dn
	}
	return slug
}

const modelBrandColor = "#8ec07c"

func modelColorFor(name string) string {
	if name == "" {
		return ""
	}
	return modelBrandColor
}

func setResult(ai *transcript.Chunk, callID, output string) {
	if ai == nil {
		return
	}
	for i := range ai.Items {
		if ai.Items[i].ToolID == callID {
			ai.Items[i].Result = output
			if code, ok := execExitCode(output); ok {
				ai.Items[i].ResultIsError = code != 0
			}
			return
		}
	}
}

func execExitCode(output string) (code int, ok bool) {
	return exitCodeAfter(output, "Process exited with code ")
}

func exitCodeAfter(text, marker string) (code int, ok bool) {
	i := strings.Index(text, marker)
	if i < 0 {
		return 0, false
	}
	rest := text[i+len(marker):]
	if j := strings.IndexByte(rest, '\n'); j >= 0 {
		rest = rest[:j]
	}
	code, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil {
		return 0, false
	}
	return code, true
}

func spawnArgs(raw json.RawMessage) (agentType, message string) {
	var a struct {
		AgentType string `json:"agent_type"`
		Message   string `json:"message"`
	}
	_ = json.Unmarshal([]byte(argString(raw)), &a)
	return a.AgentType, a.Message
}

func spawnResult(output string) (agentID, nickname string) {
	var o struct {
		AgentID  string `json:"agent_id"`
		Nickname string `json:"nickname"`
	}
	if json.Unmarshal([]byte(output), &o) != nil {
		return "", ""
	}
	return o.AgentID, o.Nickname
}

func setSpawnResult(ai *transcript.Chunk, callID, agentID, nickname string) {
	if ai == nil {
		return
	}
	for i := range ai.Items {
		if ai.Items[i].ToolID == callID {
			if len(ai.Items[i].Subagents) == 0 {
				ai.Items[i].Subagents = []transcript.Subagent{{}}
			}
			ai.Items[i].Subagents[0].ID = agentID
			ai.Items[i].Subagents[0].Name = nickname
			return
		}
	}
}

func waitCloseTargets(name string, raw json.RawMessage) []string {
	switch name {
	case "wait_agent":
		var a struct {
			Targets []string `json:"targets"`
		}
		_ = json.Unmarshal([]byte(argString(raw)), &a)
		return a.Targets
	case "close_agent":
		var a struct {
			Target string `json:"target"`
		}
		_ = json.Unmarshal([]byte(argString(raw)), &a)
		if a.Target == "" {
			return nil
		}
		return []string{a.Target}
	default:
		return nil
	}
}

func buildSubagents(ids []string, known map[string]string) []transcript.Subagent {
	if len(ids) == 0 {
		return nil
	}
	out := make([]transcript.Subagent, len(ids))
	for i, id := range ids {
		out[i] = transcript.Subagent{ID: id, Name: known[id]}
	}
	return out
}

// arguments is a JSON-encoded string; decode the outer quoting.
func argString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

type outputContentBlock struct {
	Text     string `json:"text"`
	ImageURL string `json:"image_url"`
	Detail   string `json:"detail"`
}

// Image blocks are collapsed to placeholders.
func outputText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []outputContentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch {
		case b.Text != "":
			parts = append(parts, b.Text)
		case b.ImageURL != "":
			if b.Detail != "" {
				parts = append(parts, "[image, detail: "+b.Detail+"]")
			} else {
				parts = append(parts, "[image]")
			}
		}
	}
	return strings.Join(parts, "\n")
}

// Returns "" for encrypted or non-array values.
func summaryText(raw json.RawMessage) string {
	var parts []rolloutSummary
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(p.Text)
	}
	return strings.TrimSpace(b.String())
}

func applyTokenCount(c *transcript.Chunk, info *tokenInfo, firstCtx map[*transcript.Chunk]int) {
	last := info.Last
	// A Codex AI chunk spans a whole turn (many round-trips); accumulate per-round
	// output, but take input/cache from the latest snapshot (current context).
	c.Usage.Input = last.InputTokens - last.CachedInputTokens
	c.Usage.CacheRead = last.CachedInputTokens
	c.Usage.Output += last.OutputTokens
	if info.ModelContextWindow > 0 {
		pct := float64(info.Total.InputTokens) / float64(info.ModelContextWindow) * 100
		if !c.HasContext {
			c.HasContext = true
			c.ContextFirstPct = pct
			firstCtx[c] = info.Total.InputTokens
		}
		c.ContextPct = pct
		if d := info.Total.InputTokens - firstCtx[c]; d > 0 {
			c.ContextDeltaTokens = d
		}
	}
}

func stampIDs(chunks []transcript.Chunk) {
	for i := range chunks {
		chunks[i].ID = strconv.Itoa(i)
		for j := range chunks[i].Items {
			chunks[i].Items[j].ID = strconv.Itoa(j)
			if chunks[i].Items[j].Kind == transcript.ItemSkill && chunks[i].Items[j].ToolID == "" {
				chunks[i].Items[j].ToolID = "skill:" + strconv.Itoa(i) + ":" + strconv.Itoa(j)
			}
		}
	}
}
