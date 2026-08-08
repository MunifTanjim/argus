package parser

// SessionDirKeys holds the snake_case session_id values that can key a session's
// on-disk tasks/ and teams/ dirs. Claude Code has keyed these dirs differently
// across versions and resume/fork chains: sometimes by the transcript filename,
// sometimes by the root (first) session_id, sometimes by the id on the line that
// wrote the board. No single value is correct everywhere, so callers probe every
// candidate (plus the filename) and use the dir that exists.
type SessionDirKeys struct {
	Tasks string // session_id of the last task-mutating tool line (TaskCreate/TaskUpdate/TaskStop)
	Teams string // session_id of the last team line (TeamCreate or a team member spawn)
	First string // first non-empty session_id seen (the root)
	Last  string // last non-empty session_id seen
}

// TasksCandidates returns the session_id keys to probe for the tasks dir, most
// likely first, empties and duplicates removed. The root (First) leads because
// the id on the last task-writing line proved the least reliable in practice: it
// can point at a stale or non-existent dir while the live board sits under the
// root. Since the caller returns the first candidate dir that exists and holds
// tasks, ordering only decides when two candidate dirs are both populated, and
// there the root must win. The caller appends the transcript filename as a final
// fallback.
func (k SessionDirKeys) TasksCandidates() []string {
	return dedupeNonEmpty(k.First, k.Tasks, k.Last)
}

// TeamsCandidates returns the session_id keys to probe for the teams dir, root first.
func (k SessionDirKeys) TeamsCandidates() []string {
	return dedupeNonEmpty(k.First, k.Teams, k.Last)
}

func dedupeNonEmpty(vals ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range vals {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

var taskMutatingTools = map[string]struct{}{
	"TaskCreate": {},
	"TaskUpdate": {},
	"TaskStop":   {},
}

// IsTaskMutatingTool reports whether name is a tool that writes the task board.
func IsTaskMutatingTool(name string) bool {
	_, ok := taskMutatingTools[name]
	return ok
}

// ResolveSessionDirKeys walks the chunks and returns the candidate session_id
// keys for the on-disk dirs. Tasks/Teams take the last line that wrote each dir
// (last-wins); First/Last bound the session_ids seen and are sourced at chunk
// level so a user-only transcript (e.g. right after a resume, before the first
// assistant turn) still yields a key.
func ResolveSessionDirKeys(chunks []Chunk) SessionDirKeys {
	var keys SessionDirKeys
	for ci := range chunks {
		if id := chunks[ci].SessionID; id != "" {
			if keys.First == "" {
				keys.First = id
			}
			keys.Last = id
		}
		for i := range chunks[ci].Items {
			it := &chunks[ci].Items[i]
			if it.SessionID == "" {
				continue
			}
			switch {
			case IsTaskMutatingTool(it.ToolName):
				keys.Tasks = it.SessionID
			case it.ToolName == "TeamCreate" || (it.Type == ItemSubagent && IsTeamTask(it)):
				keys.Teams = it.SessionID
			}
		}
	}
	return keys
}
