package codex

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/MunifTanjim/argus/internal/codextool"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func parseRollout(path string, finished bool) ([]transcript.Entry, error) {
	lines, err := scanRollout(path)
	if err != nil {
		return nil, err
	}
	return foldRollout(lines, loadModelNames(), finished), nil
}

// foldRollout is pure in its inputs, so streaming can re-fold an accumulating
// line slice each Refresh. finished closes the final turn even if it never
// completed (history reads).
func foldRollout(lines []rolloutLine, models map[string]string, finished bool) []transcript.Entry {
	lines = ownLines(lines)
	var out []transcript.Entry
	model := ""
	nicknames := map[string]string{} // agent id -> nickname, from each spawn seen so far
	// Open code-mode "exec" calls, in call order. Entries only grow or change in
	// place (subscribers rely on stable IDs), so a script that calls nothing but
	// exec_command has its row taken over by its first recorded command.
	var execOpen []*execCall
	// Multi-agent v2 records each subagent transition as a SubAgentActivity item.
	activityChild := map[string]string{} // call id -> child thread id
	activityKind := map[string]string{}  // child thread id -> latest kind

	// Footer of the open turn, closed by task_complete or the next user
	// boundary. Aborted/usage-only turns add no entries and get no footer.
	var turn transcript.Turn
	turnLastTS := "" // timestamp of the turn's last entry: the footer's fallback
	firstCtx := 0
	calls := map[string]int{}    // call id -> index into out
	turnSeq := 0                 // sequence of the open (or last opened) turn
	callTurn := map[string]int{} // call id -> turn that counted it
	footerAt := map[int]int{}    // turn -> index of its emitted footer in out
	ensureTurn := func() *transcript.Entry {
		if turn.Footer == nil {
			turnSeq++
			turn.Open(transcript.Entry{Kind: transcript.EntryTurnEnd, ModelName: model, ModelColor: modelColorFor(model)})
			turnLastTS, firstCtx = "", 0
		}
		return turn.Footer
	}
	endTurn := func() {
		if f, ok := turn.Close(); ok {
			if f.Timestamp == "" {
				f.Timestamp = turnLastTS // closed by a user boundary, not task_complete
			}
			footerAt[turnSeq] = len(out)
			out = append(out, f)
		}
	}
	// uncount drops a call from the tool count of the turn that counted it,
	// whether that turn is still open or its footer was already emitted.
	uncount := func(callID string) {
		seq, ok := callTurn[callID]
		if !ok {
			return
		}
		f := turn.Footer
		if seq != turnSeq || f == nil {
			f = nil
			if i, ok := footerAt[seq]; ok {
				f = &out[i]
			}
		}
		if f != nil && f.ToolCount > 0 {
			f.ToolCount--
		}
	}
	add := func(e transcript.Entry) {
		if e.Kind == transcript.EntryTool && e.InputPreview == "" {
			e.InputPreview = toolPreview(e.ToolName, e.ToolInput)
		}
		ensureTurn()
		turn.Count(e)
		if e.ToolID != "" {
			calls[e.ToolID] = len(out)
			callTurn[e.ToolID] = turnSeq
		}
		out = append(out, e)
		if e.Timestamp != "" {
			turnLastTS = e.Timestamp
		}
	}
	call := func(callID string) *transcript.Entry {
		if i, ok := calls[callID]; ok {
			return &out[i]
		}
		return nil
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
					applyTokenCount(ensureTurn(), p.Info, &firstCtx)
				}
			case "task_complete":
				if t := turn.Footer; t != nil {
					if p.DurationMs > 0 {
						t.DurationMs = p.DurationMs
					}
					t.Timestamp = l.Timestamp
				}
				endTurn()
			case "item_completed":
				if p.Item != nil && p.Item.Type == "SubAgentActivity" && p.Item.AgentThreadID != "" {
					activityChild[p.Item.ID] = p.Item.AgentThreadID
					activityKind[p.Item.AgentThreadID] = p.Item.Kind
				}
				// A command a code-mode script ran; attributed to the latest open exec.
				if p.Item != nil && p.Item.Type == "CommandExecution" && p.Item.Source == "unified_exec_startup" && len(execOpen) > 0 {
					x := execOpen[len(execOpen)-1]
					cmd := nestedCommandEntry(p.Item, l.Timestamp)
					if x.slot >= 0 {
						// Take over the exec row in place: same position and count.
						delete(calls, x.id)
						calls[cmd.ToolID] = x.slot
						callTurn[cmd.ToolID] = callTurn[x.id]
						cmd.ID = out[x.slot].ID
						out[x.slot] = cmd
						x.slot, x.replaced = -1, true
					} else {
						add(cmd)
					}
				}
			}
		case "response_item":
			switch p.Type {
			case "agent_message":
				// Another agent's message starts this agent's turn, as a user
				// prompt starts a session's.
				endTurn()
				out = append(out, transcript.Entry{Kind: transcript.EntryUser, Timestamp: l.Timestamp, Text: agentMessageText(p)})
			case "message":
				switch p.Role {
				case "user":
					text := contentText(p.Content)
					if isScaffolding(text) {
						continue
					}
					endTurn()
					if cmd, result, ok := userShellCommand(text); ok {
						code, _ := exitCodeAfter(result, "Exit code: ")
						out = append(out, transcript.Entry{
							Kind: transcript.EntryShell, Timestamp: l.Timestamp,
							Text: cmd, Detail: result, IsError: code != 0,
						})
						continue
					}
					if name, _, body, ok := skillLoad(text); ok {
						input, _ := json.Marshal(map[string]string{"skill": name})
						out = append(out, transcript.Entry{
							Kind:         transcript.EntrySkill,
							Timestamp:    l.Timestamp,
							ToolName:     "Skill",
							ToolID:       p.ID,
							ToolInput:    string(input),
							InputPreview: name,
							Result:       body,
						})
						continue
					}
					out = append(out, transcript.Entry{Kind: transcript.EntryUser, Timestamp: l.Timestamp, Text: text})
				case "assistant":
					if text := contentText(p.Content); strings.TrimSpace(text) != "" {
						add(transcript.Entry{Kind: transcript.EntryText, Timestamp: l.Timestamp, Text: text})
					}
				}
			case "reasoning":
				// Show a thinking entry even when summary is empty (encrypted reasoning).
				add(transcript.Entry{Kind: transcript.EntryThinking, Timestamp: l.Timestamp, Text: summaryText(p.Summary)})
			case "function_call":
				e := transcript.Entry{
					Kind:      transcript.EntryTool,
					Timestamp: l.Timestamp,
					ToolName:  codexToolName(p.Namespace, p.Name),
					ToolID:    p.CallID,
					ToolInput: argString(p.Arguments),
				}
				switch {
				case p.Name == "spawn_agent":
					e.Kind = transcript.EntrySubagent
					typ, desc := spawnArgs(p.Arguments)
					e.Subagents = []transcript.Subagent{{Type: typ, Desc: desc}}
				case (p.Name == "wait_agent" || p.Name == "close_agent") && p.Namespace != multiAgentV2Namespace:
					// v1 names its target agents by thread id; v2's wait_agent names none.
					e.Kind = transcript.EntrySubagent
					e.Subagents = buildSubagents(waitCloseTargets(p.Name, p.Arguments), nicknames)
				}
				add(e)
			case "function_call_output":
				res := outputText(p.Output)
				e := call(p.CallID)
				setResult(e, res)
				if inlineAcceptedAsyncQuestion(e, res) {
					uncount(p.CallID)
				}
				if id, nick, _ := spawnResult(res); id != "" {
					setSpawnResult(e, id, nick)
					if nick != "" {
						nicknames[id] = nick
					}
				}
			case "custom_tool_call":
				e := transcript.Entry{
					Kind:      transcript.EntryTool,
					Timestamp: l.Timestamp,
					ToolName:  p.Name,
					ToolID:    p.CallID,
					ToolInput: p.Input,
				}
				if p.Name != "exec" {
					add(e)
					break
				}
				preview, only := scanCodeMode(p.Input)
				e.InputPreview = preview
				add(e)
				x := &execCall{id: p.CallID, input: p.Input, slot: -1}
				if only {
					x.slot = calls[p.CallID]
				}
				execOpen = append(execOpen, x)
			case "custom_tool_call_output":
				res := outputText(p.Output)
				i := slices.IndexFunc(execOpen, func(x *execCall) bool { return x.id == p.CallID })
				if i < 0 {
					setResult(call(p.CallID), res)
					break
				}
				x := execOpen[i]
				execOpen = slices.Delete(execOpen, i, i+1)
				if !x.replaced {
					setResult(call(p.CallID), res)
					break
				}
				// The row now shows a command; a failed script gets its own row so
				// the error stays visible. A successful script's output is dropped,
				// as in the Codex TUI.
				if isErr, _ := resultIsError("exec", res); isErr {
					add(transcript.Entry{
						Kind: transcript.EntryTool, Timestamp: l.Timestamp,
						ToolName: "exec", ToolID: x.id, ToolInput: x.input,
						Result: res, ResultIsError: true,
					})
				}
			case "web_search_call":
				// No paired output event; web_search_end is ignored as a duplicate.
				add(transcript.Entry{
					Kind:      transcript.EntryTool,
					Timestamp: l.Timestamp,
					ToolName:  "web_search",
					ToolID:    p.ID,
					ToolInput: string(p.Action),
				})
			}
		}
	}
	if finished {
		endTurn()
	}
	applySubagentActivity(out, activityChild, activityKind)
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

func setResult(e *transcript.Entry, output string) {
	if e == nil {
		return
	}
	e.Result = output
	if isErr, ok := resultIsError(e.ToolName, output); ok {
		e.ResultIsError = isErr
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

// spawnArgs reads a spawn_agent call's agent type and task description. Multi-
// agent v2 names each task and may send the message encrypted; the task name
// stands in for an unreadable message.
func spawnArgs(raw json.RawMessage) (agentType, desc string) {
	var a struct {
		AgentType string `json:"agent_type"`
		Message   string `json:"message"`
		TaskName  string `json:"task_name"`
	}
	_ = json.Unmarshal([]byte(argString(raw)), &a)
	desc = a.Message
	if desc == "" || strings.HasPrefix(desc, codextool.EncryptedPrefix) {
		desc = a.TaskName
	}
	return a.AgentType, desc
}

// spawnResult reads a spawn_agent output. Multi-agent v1 returns the child's
// thread id; v2 returns only its agent path (task_name), which stampSubagents
// resolves to a thread.
func spawnResult(output string) (agentID, nickname, agentPath string) {
	var o struct {
		AgentID  string `json:"agent_id"`
		Nickname string `json:"nickname"`
		TaskName string `json:"task_name"`
	}
	if json.Unmarshal([]byte(output), &o) != nil {
		return "", "", ""
	}
	return o.AgentID, o.Nickname, o.TaskName
}

func setSpawnResult(e *transcript.Entry, agentID, nickname string) {
	if e == nil {
		return
	}
	if len(e.Subagents) == 0 {
		e.Subagents = []transcript.Subagent{{}}
	}
	e.Subagents[0].ID = agentID
	e.Subagents[0].Name = nickname
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

func applyTokenCount(e *transcript.Entry, info *tokenInfo, firstCtx *int) {
	last := info.Last
	// A Codex turn spans many round-trips; accumulate per-round output, but take
	// input/cache from the latest snapshot (current context).
	e.Usage.Input = last.InputTokens - last.CachedInputTokens
	e.Usage.CacheRead = last.CachedInputTokens
	e.Usage.Output += last.OutputTokens
	if info.ModelContextWindow > 0 {
		pct := float64(info.Total.InputTokens) / float64(info.ModelContextWindow) * 100
		if !e.HasContext {
			e.HasContext = true
			e.ContextFirstPct = pct
			*firstCtx = info.Total.InputTokens
		}
		e.ContextPct = pct
		if d := info.Total.InputTokens - *firstCtx; d > 0 {
			e.ContextDeltaTokens = d
		}
	}
}

func stampIDs(out []transcript.Entry) {
	for i := range out {
		out[i].ID = strconv.Itoa(i)
		if out[i].Kind == transcript.EntrySkill && out[i].ToolID == "" {
			out[i].ToolID = "skill:" + strconv.Itoa(i)
		}
	}
}

// applySubagentActivity links v2 spawns to their child thread through the
// "started" activity (its id is the spawn call's) and sets each child's status
// from its latest activity.
func applySubagentActivity(entries []transcript.Entry, childOf, kindOf map[string]string) {
	if len(childOf) == 0 {
		return
	}
	for i := range entries {
		e := &entries[i]
		if baseToolName(e.ToolName) != "spawn_agent" || len(e.Subagents) == 0 {
			continue
		}
		sub := &e.Subagents[0]
		if sub.ID == "" {
			sub.ID = childOf[e.ToolID]
		}
		if st := activityStatus(kindOf[sub.ID]); st != "" {
			sub.Status = st
		}
	}
}

// activityStatus names a subagent's state after a SubAgentActivity kind.
func activityStatus(kind string) string {
	switch kind {
	case "started", "interacted":
		return "running"
	case "completed":
		return "completed"
	case "interrupted":
		return "interrupted"
	}
	return ""
}

// agentMessageText renders an inter-agent message: its type and sender from
// the readable header ("Message Type: NEW_TASK\n…Payload:\n"), then the payload,
// which Codex usually sends encrypted.
func agentMessageText(p rolloutPayload) string {
	var text strings.Builder
	encrypted := false
	for _, c := range p.Content {
		switch c.Type {
		case "input_text", "output_text":
			text.WriteString(c.Text)
		case "encrypted_content":
			encrypted = true
		}
	}
	head, body, _ := strings.Cut(text.String(), "Payload:")
	kind := "Message"
	for _, ln := range strings.Split(head, "\n") {
		if v, ok := strings.CutPrefix(ln, "Message Type: "); ok && v != "" {
			kind = strings.ReplaceAll(strings.ToLower(v), "_", " ")
			kind = strings.ToUpper(kind[:1]) + kind[1:]
		}
	}
	line := kind
	if p.Author != "" {
		line += " from " + p.Author
	}
	switch body = strings.TrimSpace(body); {
	case body != "":
		return line + "\n\n" + body
	case encrypted:
		return line + " (message encrypted)"
	}
	return line
}
