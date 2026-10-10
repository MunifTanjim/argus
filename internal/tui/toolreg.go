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
	agent    string
	display  string
	category toolCategory
	detail   func(m model, it transcript.Entry, width int) string
}

// Unregistered tools fall back to the generic body and misc icon.
var toolRegistry = map[string]toolMeta{
	// antigravity
	"run_command":                {agentAntigravity, "Run Command", catBash, (model).runCommandDetail},
	"grep_search":                {agentAntigravity, "Grep Search", catGrep, (model).grepSearchDetail},
	"list_dir":                   {agentAntigravity, "List Dir", catGlob, (model).listDirDetail},
	"view_file":                  {agentAntigravity, "View File", catRead, (model).viewFileDetail},
	"write_to_file":              {agentAntigravity, "Write to File", catWrite, (model).writeToFileDetail},
	"replace_file_content":       {agentAntigravity, "Replace File Content", catEdit, (model).replaceFileContentDetail},
	"multi_replace_file_content": {agentAntigravity, "Multi Replace File Content", catEdit, (model).multiReplaceFileContentDetail},
	"search_web":                 {agentAntigravity, "Search Web", catWeb, (model).searchWebDetail},
	"generate_image":             {agentAntigravity, "Generate Image", catOther, (model).generateImageDetail},
	"invoke_subagent":            {agentAntigravity, "Invoke Subagent", catTask, nil}, // ItemSubagent: rendered by the subagent view
	"define_subagent":            {agentAntigravity, "Define Subagent", catTask, (model).defineSubagentDetail},
	"manage_subagents":           {agentAntigravity, "Manage Subagents", catTask, (model).manageSubagentsDetail},
	"manage_task":                {agentAntigravity, "Manage Task", catOther, (model).manageTaskDetail},
	"ask_question":               {agentAntigravity, "Ask Question", catOther, (model).askQuestionDetail},
	"ask_permission":             {agentAntigravity, "Ask Permission", catOther, (model).askPermissionDetail},
	"list_permissions":           {agentAntigravity, "List Permissions", catOther, (model).listPermissionsDetail},
	"send_message":               {agentAntigravity, "Send Message", catOther, (model).sendMessageDetail},
	"schedule":                   {agentAntigravity, "Schedule", catOther, (model).scheduleDetail},

	// claude code
	"Read":            {agentClaude, "", catRead, (model).readDetail},
	"NotebookRead":    {agentClaude, "", catRead, (model).readDetail},
	"Edit":            {agentClaude, "", catEdit, (model).editToolDetail},
	"MultiEdit":       {agentClaude, "", catEdit, (model).editToolDetail},
	"NotebookEdit":    {agentClaude, "", catEdit, (model).editToolDetail},
	"Write":           {agentClaude, "", catWrite, (model).editToolDetail},
	"Bash":            {agentClaude, "", catBash, (model).bashDetail},
	"BashOutput":      {agentClaude, "", catBash, nil},
	"KillShell":       {agentClaude, "", catBash, nil},
	"Grep":            {agentClaude, "", catGrep, (model).grepDetail},
	"Glob":            {agentClaude, "", catGlob, (model).globDetail},
	"LS":              {agentClaude, "", catGlob, (model).globDetail},
	"WebFetch":        {agentClaude, "", catWeb, (model).webDetail},
	"WebSearch":       {agentClaude, "", catWeb, (model).webDetail},
	"AskUserQuestion": {agentClaude, "", catOther, (model).askUserQuestionDetail},
	"ExitPlanMode":    {agentClaude, "", catOther, nil},
	"EnterPlanMode":   {agentClaude, "", catOther, nil},
	"TodoWrite":       {agentClaude, "", catTodo, (model).todoDetail},
	"TaskCreate":      {agentClaude, "Task Create", catTodo, (model).taskCreateDetail},
	"TaskUpdate":      {agentClaude, "Task Update", catTodo, (model).taskUpdateDetail},
	"TaskList":        {agentClaude, "Task List", catTodo, nil},
	"TaskGet":         {agentClaude, "Task Get", catTodo, nil},
	"TaskOutput":      {agentClaude, "Task Output", catTodo, nil},
	"TaskStop":        {agentClaude, "Task Stop", catTodo, nil},
	"ToolSearch":      {agentClaude, "Tool Search", catGrep, nil},
	"LSP":             {agentClaude, "LSP", catOther, nil},
	"Task":            {agentClaude, "", catTask, nil},  // ItemSubagent: subagent view
	"Agent":           {agentClaude, "", catTask, nil},  // ItemSubagent: subagent view
	"Skill":           {agentClaude, "", catSkill, nil}, // ItemSubagent: subagent view

	// codex
	"exec_command":             {agentCodex, "Exec Command", catBash, (model).execCommandDetail},
	"apply_patch":              {agentCodex, "Apply Patch", catEdit, (model).applyPatchDetail},
	"update_plan":              {agentCodex, "Update Plan", catOther, (model).planDetail},
	"view_image":               {agentCodex, "View Image", catRead, (model).viewImageDetail},
	"exec":                     {agentCodex, "Exec", catBash, (model).codexExecDetail},
	"request_user_input":       {agentCodex, "Question", catOther, (model).codexQuestionDetail},
	"request_user_input_async": {agentCodex, "Async Question", catOther, (model).codexAsyncQuestionDetail},
	"web_search":               {agentCodex, "Web Search", catWeb, (model).webDetail},
	"wait_agent":               {agentCodex, "Wait Agent", catTask, (model).waitAgentDetail},   // ItemSubagent: status view
	"close_agent":              {agentCodex, "Close Agent", catTask, (model).closeAgentDetail}, // ItemSubagent: status view
	"spawn_agent":              {agentCodex, "Spawn Agent", catTask, nil},                      // ItemSubagent: rendered by the subagent view
	"wait":                     {agentCodex, "Wait", catBash, (model).codexWaitCellDetail},
	"clock.sleep":              {agentCodex, "Sleep", catOther, (model).codexSleepDetail},
	// codex multi-agent v2: agents are addressed by path
	"collaboration.spawn_agent":     {agentCodex, "Spawn Agent", catTask, nil},
	"collaboration.wait_agent":      {agentCodex, "Wait Agent", catTask, (model).codexV2WaitAgentDetail},
	"collaboration.send_message":    {agentCodex, "Send Message", catTask, (model).codexAgentMessageDetail},
	"collaboration.followup_task":   {agentCodex, "Follow-up Task", catTask, (model).codexAgentMessageDetail},
	"collaboration.interrupt_agent": {agentCodex, "Interrupt Agent", catTask, (model).codexInterruptAgentDetail},
	"collaboration.list_agents":     {agentCodex, "List Agents", catTask, (model).codexListAgentsDetail},

	// opencode (lowercase tool names, distinct from the other agents' keys)
	"read":      {agentOpenCode, "Read", catRead, (model).readDetail},
	"edit":      {agentOpenCode, "Edit", catEdit, (model).editToolDetail},
	"write":     {agentOpenCode, "Write", catWrite, (model).editToolDetail},
	"bash":      {agentOpenCode, "Bash", catBash, (model).bashDetail},
	"shell":     {agentOpenCode, "Shell", catBash, (model).bashDetail},
	"execute":   {agentOpenCode, "Execute", catBash, (model).opencodeExecuteDetail},
	"grep":      {agentOpenCode, "Grep", catGrep, (model).grepDetail},
	"glob":      {agentOpenCode, "Glob", catGlob, (model).globDetail},
	"webfetch":  {agentOpenCode, "Webfetch", catWeb, (model).webDetail},
	"websearch": {agentOpenCode, "Websearch", catWeb, (model).webDetail},
	"todowrite": {agentOpenCode, "Todo", catTodo, (model).todoDetail},
	"skill":     {agentOpenCode, "Skill", catSkill, (model).opencodeSkillDetail},
	"task":      {agentOpenCode, "Task", catTask, (model).opencodeTaskDetail},
	"subagent":  {agentOpenCode, "Subagent", catTask, (model).opencodeTaskDetail},
	"question":  {agentOpenCode, "Question", catOther, (model).opencodeQuestionDetail},
}

// lookupTool resolves a tool's registry entry. MCP tools (mcp__<server>__<tool>),
// named the same by every agent, share one entry whose display name is
// "server › tool".
func lookupTool(name string) (toolMeta, bool) {
	if meta, ok := toolRegistry[name]; ok {
		return meta, true
	}
	if display, ok := transcript.MCPDisplayName(name); ok {
		return toolMeta{display: display, category: catOther, detail: (model).mcpDetail}, true
	}
	return toolMeta{}, false
}
