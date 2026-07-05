package tui

import (
	"image/color"

	"github.com/MunifTanjim/argus/internal/transcript"
)

const (
	agentClaude      = "claude"
	agentCodex       = "codex"
	agentAntigravity = "antigravity"
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
	case catSkill:
		return Icon.Tool.Skill
	case catWeb:
		return Icon.Tool.Web
	default:
		return Icon.Tool.Misc
	}
}

func categoryColor(c toolCategory) color.Color {
	switch c {
	case catRead:
		return ColorToolRead
	case catEdit:
		return ColorToolEdit
	case catWrite:
		return ColorToolWrite
	case catBash:
		return ColorToolBash
	case catGrep:
		return ColorToolGrep
	case catGlob:
		return ColorToolGlob
	case catTask:
		return ColorToolTask
	case catSkill:
		return ColorToolSkill
	case catWeb:
		return ColorToolWeb
	default:
		return ColorToolOther
	}
}

type toolMeta struct {
	agent    string
	display  string
	category toolCategory
	detail   func(m model, it transcript.Item, width int) string
}

// toolRegistry is the authoritative tool→agent map. Claude Code and Codex tools
// still live in the toolIcon/toolColor/toolDisplayName/toolDetailBody switches
// (to migrate here later); for now it holds Antigravity's tools. All lookups
// consult this map first and fall back to those switches.
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
}
