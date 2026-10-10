import 'package:flutter/material.dart';

import '../models/entry.dart';
import 'theme.dart';
import 'tool_detail_antigravity.dart';
import 'tool_detail_claude.dart';
import 'tool_detail.dart';
import 'tool_detail_codex.dart';

const _blue = Color(0xFF83a598);
const _green = Color(0xFFb8bb26);
const _yellow = Color(0xFFfabd2f);
const _orange = Color(0xFFfe8019);
const _purple = Color(0xFFd3869b);

/// A tool's icon/color category.
enum ToolCategory {
  other,
  read,
  edit,
  write,
  bash,
  grep,
  glob,
  task,
  todo,
  skill,
  web
}

IconData categoryIcon(ToolCategory c) => switch (c) {
      ToolCategory.read => Icons.menu_book_outlined,
      ToolCategory.edit => Icons.edit_outlined,
      ToolCategory.write => Icons.note_add_outlined,
      ToolCategory.bash => Icons.terminal,
      ToolCategory.grep => Icons.search,
      ToolCategory.glob => Icons.folder_open_outlined,
      ToolCategory.task => Icons.smart_toy_outlined,
      ToolCategory.todo => Icons.checklist,
      ToolCategory.skill => Icons.school_outlined,
      ToolCategory.web => Icons.public,
      ToolCategory.other => Icons.play_arrow,
    };

Color categoryColor(ToolCategory c) => switch (c) {
      ToolCategory.read || ToolCategory.web => _blue,
      ToolCategory.edit || ToolCategory.todo => _yellow,
      ToolCategory.write => _green,
      ToolCategory.bash || ToolCategory.skill => _orange,
      ToolCategory.grep => _purple,
      ToolCategory.glob || ToolCategory.task || ToolCategory.other =>
        AppColors.accent,
    };

typedef ToolDetailBuilder = Widget Function(Entry item);

class ToolMeta {
  const ToolMeta(this.display, this.category, [this.detail]);
  final String display;
  final ToolCategory category;
  final ToolDetailBuilder? detail;
}

/// Each agent's tools. Lookup prefers the session's agent, so two agents may
/// use one name for different tools. Unregistered tools fall back to generic
/// rendering.
final Map<String, Map<String, ToolMeta>> toolRegistry = {
  'antigravity': {
    'run_command': const ToolMeta(
        'Run Command', ToolCategory.bash, agyRunCommandDetail),
    'grep_search': const ToolMeta(
        'Grep Search', ToolCategory.grep, agyGrepSearchDetail),
    'list_dir':
        const ToolMeta('List Dir', ToolCategory.glob, agyListDirDetail),
    'view_file':
        const ToolMeta('View File', ToolCategory.read, agyViewFileDetail),
    'write_to_file': const ToolMeta(
        'Write to File', ToolCategory.write, agyWriteToFileDetail),
    'replace_file_content': const ToolMeta('Replace File Content',
        ToolCategory.edit, agyReplaceFileContentDetail),
    'multi_replace_file_content': const ToolMeta('Multi Replace File Content',
        ToolCategory.edit, agyMultiReplaceFileContentDetail),
    'search_web':
        const ToolMeta('Search Web', ToolCategory.web, agySearchWebDetail),
    'generate_image': const ToolMeta(
        'Generate Image', ToolCategory.other, agyGenerateImageDetail),
    'invoke_subagent': const ToolMeta('Invoke Subagent', ToolCategory.task),
    'define_subagent': const ToolMeta(
        'Define Subagent', ToolCategory.task, agyDefineSubagentDetail),
    'manage_subagents': const ToolMeta(
        'Manage Subagents', ToolCategory.task, agyManageSubagentsDetail),
    'manage_task':
        const ToolMeta('Manage Task', ToolCategory.other, agyManageTaskDetail),
    'ask_question':
        const ToolMeta('Ask Question', ToolCategory.other, agyAskQuestionDetail),
    'ask_permission': const ToolMeta(
        'Ask Permission', ToolCategory.other, agyAskPermissionDetail),
    'list_permissions': const ToolMeta(
        'List Permissions', ToolCategory.other, agyListPermissionsDetail),
    'send_message': const ToolMeta(
        'Send Message', ToolCategory.other, agySendMessageDetail),
    'schedule':
        const ToolMeta('Schedule', ToolCategory.other, agyScheduleDetail),
  },
  'codex': {
    'exec_command': const ToolMeta(
        'Exec Command', ToolCategory.bash, codexExecCommandDetail),
    'apply_patch': const ToolMeta(
        'Apply Patch', ToolCategory.edit, codexApplyPatchDetail),
    'update_plan': const ToolMeta(
        'Update Plan', ToolCategory.other, codexUpdatePlanDetail),
    'view_image': const ToolMeta(
        'View Image', ToolCategory.read, codexViewImageDetail),
    'exec': const ToolMeta('Exec', ToolCategory.bash, codexExecDetail),
    'request_user_input': const ToolMeta(
        'Question', ToolCategory.other, codexQuestionDetail),
    'request_user_input_async': const ToolMeta(
        'Async Question', ToolCategory.other, codexAsyncQuestionDetail),
    'web_search':
        const ToolMeta('Web Search', ToolCategory.web, codexWebSearchDetail),
    'wait_agent': const ToolMeta(
        'Wait Agent', ToolCategory.task, codexWaitAgentDetail),
    'close_agent': const ToolMeta(
        'Close Agent', ToolCategory.task, codexCloseAgentDetail),
    'spawn_agent': const ToolMeta('Spawn Agent', ToolCategory.task),
    'wait': const ToolMeta('Wait', ToolCategory.bash, codexWaitCellDetail),
    'clock.sleep': const ToolMeta('Sleep', ToolCategory.other, codexSleepDetail),
    // approval prompts only
    'permissions': const ToolMeta('Permissions', ToolCategory.other),
    // codex multi-agent v2: agents are addressed by path
    'collaboration.spawn_agent': const ToolMeta('Spawn Agent', ToolCategory.task),
    'collaboration.wait_agent': const ToolMeta(
        'Wait Agent', ToolCategory.task, codexV2WaitAgentDetail),
    'collaboration.send_message': const ToolMeta(
        'Send Message', ToolCategory.task, codexAgentMessageDetail),
    'collaboration.followup_task': const ToolMeta(
        'Follow-up Task', ToolCategory.task, codexAgentMessageDetail),
    'collaboration.interrupt_agent': const ToolMeta(
        'Interrupt Agent', ToolCategory.task, codexInterruptAgentDetail),
    'collaboration.list_agents': const ToolMeta(
        'List Agents', ToolCategory.task, codexListAgentsDetail),
    // skill loads, shown like Claude's Skill rows
    'Skill': const ToolMeta('', ToolCategory.skill),
  },
  'claude': {
    'Read': const ToolMeta('', ToolCategory.read),
    'NotebookRead': const ToolMeta('', ToolCategory.read),
    'Edit': const ToolMeta('', ToolCategory.edit),
    'MultiEdit': const ToolMeta('', ToolCategory.edit),
    'NotebookEdit': const ToolMeta('', ToolCategory.edit),
    'Write': const ToolMeta('', ToolCategory.write),
    'Bash': const ToolMeta('', ToolCategory.bash),
    'BashOutput': const ToolMeta('', ToolCategory.bash),
    'KillShell': const ToolMeta('', ToolCategory.bash),
    'Grep': const ToolMeta('', ToolCategory.grep),
    'Glob': const ToolMeta('', ToolCategory.glob),
    'LS': const ToolMeta('', ToolCategory.glob),
    'WebFetch': const ToolMeta('', ToolCategory.web),
    'WebSearch': const ToolMeta('', ToolCategory.web),
    'AskUserQuestion': const ToolMeta('', ToolCategory.other),
    'ExitPlanMode': const ToolMeta('', ToolCategory.other),
    'EnterPlanMode': const ToolMeta('', ToolCategory.other),
    'TodoWrite': const ToolMeta('', ToolCategory.todo),
    'TaskCreate':
        const ToolMeta('Task Create', ToolCategory.todo, claudeTaskCreateDetail),
    'TaskUpdate':
        const ToolMeta('Task Update', ToolCategory.todo, claudeTaskUpdateDetail),
    'TaskList': const ToolMeta('Task List', ToolCategory.todo),
    'TaskGet': const ToolMeta('Task Get', ToolCategory.todo),
    'TaskOutput': const ToolMeta('Task Output', ToolCategory.todo),
    'TaskStop': const ToolMeta('Task Stop', ToolCategory.todo),
    'ToolSearch': const ToolMeta('Tool Search', ToolCategory.grep),
    'LSP': const ToolMeta('LSP', ToolCategory.other),
    'Task': const ToolMeta('', ToolCategory.task),
    'Agent': const ToolMeta('', ToolCategory.task),
    'Skill': const ToolMeta('', ToolCategory.skill),
  },
  'opencode': {
    // Detail bodies for these are handled in tool_detail.dart.
    'read': const ToolMeta('Read', ToolCategory.read),
    'edit': const ToolMeta('Edit', ToolCategory.edit),
    'write': const ToolMeta('Write', ToolCategory.write),
    'bash': const ToolMeta('Bash', ToolCategory.bash),
    'shell': const ToolMeta('Shell', ToolCategory.bash),
    'execute': const ToolMeta('Execute', ToolCategory.bash),
    'grep': const ToolMeta('Grep', ToolCategory.grep),
    'glob': const ToolMeta('Glob', ToolCategory.glob),
    'webfetch': const ToolMeta('Webfetch', ToolCategory.web),
    'websearch': const ToolMeta('Websearch', ToolCategory.web),
    'todowrite': const ToolMeta('Todo', ToolCategory.todo),
    'skill': const ToolMeta('Skill', ToolCategory.skill),
    'task': const ToolMeta('Task', ToolCategory.task),
    'subagent': const ToolMeta('Subagent', ToolCategory.task),
    'question': const ToolMeta('Question', ToolCategory.other),
  },
};

/// Search order when the agent is unknown.
const _registryOrder = ['claude', 'codex', 'opencode', 'antigravity'];

/// Resolves [agent]'s entry for a tool. An unknown agent (null or empty) falls
/// back to any agent's entry, in [_registryOrder]. MCP tools resolve for every
/// agent.
ToolMeta? toolMeta(String? agent, String? name) {
  if (name == null) return null;
  final own = toolRegistry[agent]?[name];
  if (own != null) return own;
  if ((agent ?? '').isEmpty) {
    for (final a in _registryOrder) {
      final meta = toolRegistry[a]?[name];
      if (meta != null) return meta;
    }
  }
  final display = mcpDisplayName(name);
  if (display == null) return null;
  return ToolMeta(display, ToolCategory.other, mcpDetail);
}

/// Agent-reference ops render a status detail, not a trace.
bool isAgentRefTool(String? name) =>
    name == 'wait_agent' || name == 'close_agent';
