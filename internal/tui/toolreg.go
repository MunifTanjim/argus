package tui

import "github.com/MunifTanjim/argus/internal/transcript"

const (
	agentClaude      = "claude"
	agentCodex       = "codex"
	agentAntigravity = "antigravity"
	agentOpenCode    = "opencode"
)

type toolCategory int

const (
	catOther toolCategory = iota
	catRead
	catEdit
	catWrite
	catBash
	catGrep
	catGlob
	catTask
	catTodo
	catSkill
	catWeb
)

func categoryIcon(c toolCategory) StyledIcon {
	switch c {
	case catRead:
		return Icon.Tool.Read
	case catEdit:
		return Icon.Tool.Edit
	case catWrite:
		return Icon.Tool.Write
	case catBash:
		return Icon.Tool.Bash
	case catGrep:
		return Icon.Tool.Grep
	case catGlob:
		return Icon.Tool.Glob
	case catTask:
		return Icon.Tool.Task
	case catTodo:
		return Icon.Tool.Todo
	case catSkill:
		return Icon.Tool.Skill
	case catWeb:
		return Icon.Tool.Web
	default:
		return Icon.Tool.Misc
	}
}

type toolMeta struct {
	display  string
	category toolCategory
	detail   func(m model, it transcript.Entry, width int) string
}

// toolRegistry holds each agent's tools. Lookup prefers the session's agent, so
// two agents may use one name for different tools. Unregistered tools fall back
// to the generic body and misc icon.
var toolRegistry = map[string]map[string]toolMeta{
	agentClaude: {
		"Read":            {"", catRead, (model).readDetail},
		"NotebookRead":    {"", catRead, (model).readDetail},
		"Edit":            {"", catEdit, (model).editToolDetail},
		"MultiEdit":       {"", catEdit, (model).editToolDetail},
		"NotebookEdit":    {"", catEdit, (model).editToolDetail},
		"Write":           {"", catWrite, (model).editToolDetail},
		"Bash":            {"", catBash, (model).bashDetail},
		"BashOutput":      {"", catBash, nil},
		"KillShell":       {"", catBash, nil},
		"Grep":            {"", catGrep, (model).grepDetail},
		"Glob":            {"", catGlob, (model).globDetail},
		"LS":              {"", catGlob, (model).globDetail},
		"WebFetch":        {"", catWeb, (model).webDetail},
		"WebSearch":       {"", catWeb, (model).webDetail},
		"AskUserQuestion": {"", catOther, (model).askUserQuestionDetail},
		"ExitPlanMode":    {"", catOther, nil},
		"EnterPlanMode":   {"", catOther, nil},
		"TodoWrite":       {"", catTodo, (model).todoDetail},
		"TaskCreate":      {"Task Create", catTodo, (model).taskCreateDetail},
		"TaskUpdate":      {"Task Update", catTodo, (model).taskUpdateDetail},
		"TaskList":        {"Task List", catTodo, nil},
		"TaskGet":         {"Task Get", catTodo, nil},
		"TaskOutput":      {"Task Output", catTodo, nil},
		"TaskStop":        {"Task Stop", catTodo, nil},
		"ToolSearch":      {"Tool Search", catGrep, nil},
		"LSP":             {"LSP", catOther, nil},
		"Task":            {"", catTask, nil},  // ItemSubagent: subagent view
		"Agent":           {"", catTask, nil},  // ItemSubagent: subagent view
		"Skill":           {"", catSkill, nil}, // ItemSubagent: subagent view
	},
	agentCodex: {
		"exec_command":             {"Exec Command", catBash, (model).execCommandDetail},
		"apply_patch":              {"Apply Patch", catEdit, (model).applyPatchDetail},
		"update_plan":              {"Update Plan", catOther, (model).planDetail},
		"view_image":               {"View Image", catRead, (model).viewImageDetail},
		"exec":                     {"Exec", catBash, (model).codexExecDetail},
		"request_user_input":       {"Question", catOther, (model).codexQuestionDetail},
		"request_user_input_async": {"Async Question", catOther, (model).codexAsyncQuestionDetail},
		"web_search":               {"Web Search", catWeb, (model).webDetail},
		"wait_agent":               {"Wait Agent", catTask, (model).waitAgentDetail},   // ItemSubagent: status view
		"close_agent":              {"Close Agent", catTask, (model).closeAgentDetail}, // ItemSubagent: status view
		"spawn_agent":              {"Spawn Agent", catTask, nil},                      // ItemSubagent: rendered by the subagent view
		"wait":                     {"Wait", catBash, (model).codexWaitCellDetail},
		"clock.sleep":              {"Sleep", catOther, (model).codexSleepDetail},
		"permissions":              {"Permissions", catOther, nil}, // approval prompts only
		// codex multi-agent v2: agents are addressed by path
		"collaboration.spawn_agent":     {"Spawn Agent", catTask, nil},
		"collaboration.wait_agent":      {"Wait Agent", catTask, (model).codexV2WaitAgentDetail},
		"collaboration.send_message":    {"Send Message", catTask, (model).codexAgentMessageDetail},
		"collaboration.followup_task":   {"Follow-up Task", catTask, (model).codexAgentMessageDetail},
		"collaboration.interrupt_agent": {"Interrupt Agent", catTask, (model).codexInterruptAgentDetail},
		"collaboration.list_agents":     {"List Agents", catTask, (model).codexListAgentsDetail},
		"Skill":                         {"", catSkill, nil}, // skill loads, shown like Claude's Skill rows
	},
	agentOpenCode: {
		"read":      {"Read", catRead, (model).readDetail},
		"edit":      {"Edit", catEdit, (model).editToolDetail},
		"write":     {"Write", catWrite, (model).editToolDetail},
		"bash":      {"Bash", catBash, (model).bashDetail},
		"shell":     {"Shell", catBash, (model).bashDetail},
		"execute":   {"Execute", catBash, (model).opencodeExecuteDetail},
		"grep":      {"Grep", catGrep, (model).grepDetail},
		"glob":      {"Glob", catGlob, (model).globDetail},
		"webfetch":  {"Webfetch", catWeb, (model).webDetail},
		"websearch": {"Websearch", catWeb, (model).webDetail},
		"todowrite": {"Todo", catTodo, (model).todoDetail},
		"skill":     {"Skill", catSkill, (model).opencodeSkillDetail},
		"task":      {"Task", catTask, (model).opencodeTaskDetail},
		"subagent":  {"Subagent", catTask, (model).opencodeTaskDetail},
		"question":  {"Question", catOther, (model).opencodeQuestionDetail},
	},
	agentAntigravity: {
		"run_command":                {"Run Command", catBash, (model).runCommandDetail},
		"grep_search":                {"Grep Search", catGrep, (model).grepSearchDetail},
		"list_dir":                   {"List Dir", catGlob, (model).listDirDetail},
		"view_file":                  {"View File", catRead, (model).viewFileDetail},
		"write_to_file":              {"Write to File", catWrite, (model).writeToFileDetail},
		"replace_file_content":       {"Replace File Content", catEdit, (model).replaceFileContentDetail},
		"multi_replace_file_content": {"Multi Replace File Content", catEdit, (model).multiReplaceFileContentDetail},
		"search_web":                 {"Search Web", catWeb, (model).searchWebDetail},
		"generate_image":             {"Generate Image", catOther, (model).generateImageDetail},
		"invoke_subagent":            {"Invoke Subagent", catTask, nil}, // ItemSubagent: rendered by the subagent view
		"define_subagent":            {"Define Subagent", catTask, (model).defineSubagentDetail},
		"manage_subagents":           {"Manage Subagents", catTask, (model).manageSubagentsDetail},
		"manage_task":                {"Manage Task", catOther, (model).manageTaskDetail},
		"ask_question":               {"Ask Question", catOther, (model).askQuestionDetail},
		"ask_permission":             {"Ask Permission", catOther, (model).askPermissionDetail},
		"list_permissions":           {"List Permissions", catOther, (model).listPermissionsDetail},
		"send_message":               {"Send Message", catOther, (model).sendMessageDetail},
		"schedule":                   {"Schedule", catOther, (model).scheduleDetail},
	},
}

// registryOrder is the search order when the agent is unknown.
var registryOrder = []string{agentClaude, agentCodex, agentOpenCode, agentAntigravity}

// lookupTool resolves agent's entry for a tool. An unknown agent ("") falls back
// to any agent's entry, in registryOrder. MCP tools (mcp__<server>__<tool>), named
// the same by every agent, share one entry whose display name is "server › tool".
func lookupTool(agent, name string) (toolMeta, bool) {
	if meta, ok := toolRegistry[agent][name]; ok {
		return meta, true
	}
	if agent == "" {
		for _, a := range registryOrder {
			if meta, ok := toolRegistry[a][name]; ok {
				return meta, true
			}
		}
	}
	if display, ok := transcript.MCPDisplayName(name); ok {
		return toolMeta{display: display, category: catOther, detail: (model).mcpDetail}, true
	}
	return toolMeta{}, false
}
