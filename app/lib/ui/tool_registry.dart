import 'package:flutter/material.dart';

import '../models/chunk.dart';
import 'theme.dart';
import 'tool_detail_antigravity.dart';
import 'tool_detail_codex.dart';

const _blue = Color(0xFF83a598);
const _green = Color(0xFFb8bb26);
const _yellow = Color(0xFFfabd2f);
const _orange = Color(0xFFfe8019);
const _purple = Color(0xFFd3869b);

/// A tool's icon/color category. The single pairing point: [categoryIcon] and
/// [categoryColor] are its only readers, so a tool's icon and color can't drift.
enum ToolCategory { other, read, edit, write, bash, grep, glob, task, skill, web }

IconData categoryIcon(ToolCategory c) => switch (c) {
      ToolCategory.read => Icons.menu_book_outlined,
      ToolCategory.edit => Icons.edit_outlined,
      ToolCategory.write => Icons.note_add_outlined,
      ToolCategory.bash => Icons.terminal,
      ToolCategory.grep => Icons.search,
      ToolCategory.glob => Icons.folder_open_outlined,
      ToolCategory.task => Icons.smart_toy_outlined,
      ToolCategory.skill => Icons.build_outlined,
      ToolCategory.web => Icons.public,
      ToolCategory.other => Icons.play_arrow,
    };

Color categoryColor(ToolCategory c) => switch (c) {
      ToolCategory.read || ToolCategory.web => _blue,
      ToolCategory.edit => _yellow,
      ToolCategory.write => _green,
      ToolCategory.bash || ToolCategory.skill => _orange,
      ToolCategory.grep => _purple,
      ToolCategory.glob || ToolCategory.task || ToolCategory.other =>
        AppColors.accent,
    };

typedef ToolDetailBuilder = Widget Function(Item item);

class ToolMeta {
  const ToolMeta(this.display, this.category, [this.detail]);
  final String display;
  final ToolCategory category;
  final ToolDetailBuilder? detail;
}

/// The tool→meta map for Antigravity and Codex tools. Claude Code tools still
/// live in the item_row/tool_detail switches. All lookups consult this first and
/// fall back to those switches.
final Map<String, ToolMeta> toolRegistry = {
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

  'exec_command': const ToolMeta(
      'Exec Command', ToolCategory.bash, codexExecCommandDetail),
  'apply_patch': const ToolMeta('Apply Patch', ToolCategory.edit),
  'update_plan': const ToolMeta(
      'Update Plan', ToolCategory.other, codexUpdatePlanDetail),
  'view_image': const ToolMeta('View Image', ToolCategory.read),
  'web_search':
      const ToolMeta('Web Search', ToolCategory.web, codexWebSearchDetail),
  'wait_agent': const ToolMeta(
      'Wait Agent', ToolCategory.task, codexWaitAgentDetail),
  'close_agent': const ToolMeta(
      'Close Agent', ToolCategory.task, codexCloseAgentDetail),
  'spawn_agent': const ToolMeta('Spawn Agent', ToolCategory.task),
};

ToolMeta? toolMeta(String? name) => name == null ? null : toolRegistry[name];

/// Agent-reference ops render a status detail, not a trace.
bool isAgentRefTool(String? name) =>
    name == 'wait_agent' || name == 'close_agent';
