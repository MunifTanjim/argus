package parser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TeamTask represents a single task in a team's task board.
type TeamTask struct {
	ID      string // sequential within team: "1", "2", ...
	Subject string
	Status  string // "pending" | "in_progress" | "completed" | "deleted"
	Owner   string // worker name, from TaskUpdate or inferred from worker ID
}

// TeamSnapshot represents the reconstructed state of a team at the
// end of a session (or at the current point during live tailing).
type TeamSnapshot struct {
	Name          string
	Description   string
	Tasks         []TeamTask
	Members       []string          // worker names from Task spawn calls
	MemberColors  map[string]string // member name -> color name (e.g. "blue")
	MemberOngoing map[string]bool   // member name -> true if worker session is ongoing
	Deleted       bool              // true after TeamDelete
}

// ReconstructTeams replays lead and worker tool-call events into per-team task
// board state. Phase 1 walks lead chunks for Team/Task events (task IDs numbered
// from 1 per team); Phase 2 applies worker TaskUpdates; Phases 3-4 populate
// member colors and ongoing state from worker metadata.
func ReconstructTeams(chunks []Chunk, workers []SubagentProcess) []TeamSnapshot {
	var teams []TeamSnapshot
	activeIdx := -1
	taskCounter := 0

	// Phase 1: Lead chunk events.
	for i := range chunks {
		if chunks[i].Type != AIChunk {
			continue
		}
		for j := range chunks[i].Items {
			it := &chunks[i].Items[j]

			switch {
			case it.Type == ItemToolCall && it.ToolName == "TeamCreate":
				teams = append(teams, teamSnapshotFromCreate(it.ToolInput))
				activeIdx = len(teams) - 1
				taskCounter = 0

			case it.Type == ItemToolCall && it.ToolName == "TaskCreate" && activeIdx >= 0:
				taskCounter++
				teams[activeIdx].Tasks = append(teams[activeIdx].Tasks,
					teamTaskFromCreate(it.ToolInput, taskCounter))

			case it.Type == ItemToolCall && it.ToolName == "TaskUpdate" && activeIdx >= 0:
				applyTeamTaskUpdate(it.ToolInput, &teams[activeIdx])

			case it.Type == ItemToolCall && it.ToolName == "TeamDelete" && activeIdx >= 0:
				teams[activeIdx].Deleted = true
				activeIdx = -1

			case it.Type == ItemSubagent && IsTeamTask(it):
				addTeamSpawnMember(it.ToolInput, teams)
			}
		}
	}

	// Phase 2: Worker TaskUpdate events.
	for i := range workers {
		agentName, teamName := splitWorkerID(workers[i].ID)
		if teamName == "" {
			continue
		}
		team := findTeamByName(teams, teamName)
		if team == nil {
			continue
		}
		applyWorkerTaskUpdates(workers[i].Chunks, team, agentName)
	}

	// Phase 3: Populate member colors from worker metadata.
	for i := range teams {
		teams[i].MemberColors = make(map[string]string)
		teams[i].MemberOngoing = make(map[string]bool)
	}
	for _, w := range workers {
		agentName, teamName := splitWorkerID(w.ID)
		if teamName == "" || w.TeammateColor == "" {
			continue
		}
		for i := range teams {
			if teams[i].Name == teamName {
				teams[i].MemberColors[agentName] = w.TeammateColor
			}
		}
	}

	// Phase 4: Populate member ongoing state from worker sessions.
	for _, w := range workers {
		agentName, teamName := splitWorkerID(w.ID)
		if teamName == "" {
			continue
		}
		if IsOngoing(w.Chunks) {
			for i := range teams {
				if teams[i].Name == teamName {
					teams[i].MemberOngoing[agentName] = true
				}
			}
		}
	}

	return teams
}

// teamSnapshotFromCreate extracts team name and description from TeamCreate input.
func teamSnapshotFromCreate(input json.RawMessage) TeamSnapshot {
	fields := parseInputFields(input)
	return TeamSnapshot{
		Name:        getString(fields, "team_name"),
		Description: getString(fields, "description"),
	}
}

// teamTaskFromCreate extracts subject from TaskCreate input and assigns a sequential ID.
func teamTaskFromCreate(input json.RawMessage, seqID int) TeamTask {
	fields := parseInputFields(input)
	return TeamTask{
		ID:      fmt.Sprintf("%d", seqID),
		Subject: getString(fields, "subject"),
		Status:  "pending",
	}
}

// applyTeamTaskUpdate applies a TaskUpdate to the matching task in a team.
func applyTeamTaskUpdate(input json.RawMessage, team *TeamSnapshot) {
	fields := parseInputFields(input)
	taskID := getString(fields, "taskId")
	if taskID == "" {
		return
	}
	for i := range team.Tasks {
		if team.Tasks[i].ID != taskID {
			continue
		}
		if status := getString(fields, "status"); status != "" {
			team.Tasks[i].Status = status
		}
		if owner := getString(fields, "owner"); owner != "" {
			team.Tasks[i].Owner = owner
		}
		if subject := getString(fields, "subject"); subject != "" {
			team.Tasks[i].Subject = subject
		}
		return
	}
}

// addTeamSpawnMember adds a worker name to the matching team's Members list.
// Deduplicates — a worker spawned twice (e.g. resumed) appears once.
func addTeamSpawnMember(input json.RawMessage, teams []TeamSnapshot) {
	fields := parseInputFields(input)
	teamName := getString(fields, "team_name")
	memberName := getString(fields, "name")
	if teamName == "" || memberName == "" {
		return
	}
	for i := range teams {
		if teams[i].Name != teamName {
			continue
		}
		for _, m := range teams[i].Members {
			if m == memberName {
				return
			}
		}
		teams[i].Members = append(teams[i].Members, memberName)
		return
	}
}

// applyWorkerTaskUpdates applies a worker's TaskUpdate calls to the team's tasks.
// Falls back to the worker's own name as owner when the update omits one (the
// owner field is optional but workers usually claim tasks for themselves).
func applyWorkerTaskUpdates(chunks []Chunk, team *TeamSnapshot, workerName string) {
	for i := range chunks {
		if chunks[i].Type != AIChunk {
			continue
		}
		for j := range chunks[i].Items {
			it := &chunks[i].Items[j]
			if it.Type != ItemToolCall || it.ToolName != "TaskUpdate" {
				continue
			}
			fields := parseInputFields(it.ToolInput)
			taskID := getString(fields, "taskId")
			if taskID == "" {
				continue
			}
			for k := range team.Tasks {
				if team.Tasks[k].ID != taskID {
					continue
				}
				if status := getString(fields, "status"); status != "" {
					team.Tasks[k].Status = status
				}
				if owner := getString(fields, "owner"); owner != "" {
					team.Tasks[k].Owner = owner
				} else if team.Tasks[k].Owner == "" {
					team.Tasks[k].Owner = workerName
				}
				if subject := getString(fields, "subject"); subject != "" {
					team.Tasks[k].Subject = subject
				}
			}
		}
	}
}

// splitWorkerID parses "agentName@teamName" into its parts.
// Returns ("", "") for non-team worker IDs (no "@" separator).
func splitWorkerID(id string) (agentName, teamName string) {
	parts := strings.SplitN(id, "@", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// findTeamByName returns a pointer to the named team, or nil.
func findTeamByName(teams []TeamSnapshot, name string) *TeamSnapshot {
	for i := range teams {
		if teams[i].Name == name {
			return &teams[i]
		}
	}
	return nil
}

// parseInputFields unmarshals a JSON tool input into a field map.
// Returns nil on error or empty input.
func parseInputFields(input json.RawMessage) map[string]json.RawMessage {
	if len(input) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return nil
	}
	return fields
}

// ReadTeamSessionMeta reads only the first JSONL line for the top-level teamName
// and agentName fields. Returns ("", "") for non-team sessions or any error.
func ReadTeamSessionMeta(path string) (teamName, agentName string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	lr := newLineReader(f)
	line, ok := lr.next()
	if !ok {
		return "", ""
	}

	var meta struct {
		TeamName  string `json:"teamName"`
		AgentName string `json:"agentName"`
	}
	if err := json.Unmarshal([]byte(line), &meta); err != nil {
		return "", ""
	}
	return meta.TeamName, meta.AgentName
}

// teamSpec identifies a team agent spawn from the parent session.
type teamSpec struct {
	teamName  string
	agentName string
}

// extractTeamSpecs collects {teamName, agentName} pairs from Task items
// in the parent chunks where IsTeamTask returns true.
func extractTeamSpecs(chunks []Chunk) []teamSpec {
	var specs []teamSpec
	for i := range chunks {
		if chunks[i].Type != AIChunk {
			continue
		}
		for j := range chunks[i].Items {
			it := &chunks[i].Items[j]
			if it.Type != ItemSubagent || !IsTeamTask(it) {
				continue
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(it.ToolInput, &fields); err != nil {
				continue
			}
			tn := getString(fields, "team_name")
			an := getString(fields, "name")
			if tn != "" && an != "" {
				specs = append(specs, teamSpec{teamName: tn, agentName: an})
			}
		}
	}
	return specs
}

// DiscoverTeamSessions finds team agent sessions, which live as top-level
// .jsonl files in the project dir (not in subagents/) and are created by Task
// calls with team_name + name. Matches files whose first entry's teamName/agentName
// correspond to a team Task call, returning each with ID = "agentName@teamName"
// so Phase 1 of LinkSubagents can link it.
func DiscoverTeamSessions(sessionPath string, parentChunks []Chunk) ([]SubagentProcess, error) {
	specs := extractTeamSpecs(parentChunks)
	if len(specs) == 0 {
		return nil, nil
	}

	type specKey struct{ team, agent string }
	wanted := make(map[specKey]bool, len(specs))
	for _, s := range specs {
		wanted[specKey{s.teamName, s.agentName}] = true
	}

	projectDir := filepath.Dir(sessionPath)
	parentBase := filepath.Base(sessionPath)

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil, err
	}

	var procs []SubagentProcess
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		// Skip the parent session itself.
		if name == parentBase {
			continue
		}
		// Skip agent-*.jsonl files (handled by DiscoverSubagents).
		if strings.HasPrefix(name, "agent-") {
			continue
		}

		filePath := filepath.Join(projectDir, name)

		// Skip empty files.
		info, err := de.Info()
		if err != nil || info.Size() == 0 {
			continue
		}

		teamName, agentName := ReadTeamSessionMeta(filePath)
		if teamName == "" || agentName == "" {
			continue
		}
		if !wanted[specKey{teamName, agentName}] {
			continue
		}

		chunks, _, teamColor, err := readSubagentSession(filePath)
		if err != nil || len(chunks) == 0 {
			continue
		}

		startTime, endTime, durationMs := chunkTiming(chunks)
		usage := aggregateUsage(chunks)

		procs = append(procs, SubagentProcess{
			ID:            agentName + "@" + teamName,
			FilePath:      filePath,
			FileModTime:   info.ModTime(),
			Chunks:        chunks,
			StartTime:     startTime,
			EndTime:       endTime,
			DurationMs:    durationMs,
			Usage:         usage,
			Model:         extractModel(chunks),
			TeammateColor: teamColor,
		})
	}

	sort.Slice(procs, func(i, j int) bool {
		return procs[i].StartTime.Before(procs[j].StartTime)
	})

	return procs, nil
}

// filterTeamTasks returns unmatched Task items whose input contains both
// team_name and name keys, identifying them as team member spawns.
func filterTeamTasks(items []*DisplayItem, matched map[string]bool) []*DisplayItem {
	var out []*DisplayItem
	for _, it := range items {
		if matched[it.ToolID] {
			continue
		}
		if IsTeamTask(it) {
			out = append(out, it)
		}
	}
	return out
}

// IsTeamTask reports whether a Task's input has both team_name and name keys,
// marking it as a team member spawn.
func IsTeamTask(it *DisplayItem) bool {
	if len(it.ToolInput) == 0 {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(it.ToolInput, &fields); err != nil {
		return false
	}
	_, hasTeamName := fields["team_name"]
	_, hasName := fields["name"]
	return hasTeamName && hasName
}
