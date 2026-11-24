import 'dart:convert';

import 'package:flutter/material.dart';

import '../models/entry.dart';
import 'code_block.dart';
import 'edit_diff.dart';
import 'theme.dart';
import 'tool_detail_common.dart';
import 'tool_registry.dart';

Widget _resultSection(Entry it, ArgusTokens t, {bool wrap = false, String? lang}) {
  if ((it.result ?? '').isEmpty) return const SizedBox.shrink();
  return toolResult(it, codeBlock(it.result!, wrap: wrap, lang: lang), t);
}

/// Renders [agent]'s tool entry. The name-keyed renderers below apply only to
/// tools the agent registers, so another agent's same-named tool stays generic.
Widget toolDetailBody(Entry item, ArgusTokens t, {String? agent}) {
  final meta = toolMeta(agent, item.toolName);
  final detail = meta?.detail;
  if (detail != null) return detail(item, t);
  if (meta == null) return toolGeneric(item, t);
  switch (item.toolName) {
    // opencode names are lowercase; they share these renderers with the other
    // agents, and the extractors below read both key shapes.
    case 'Edit':
    case 'MultiEdit':
    case 'Write':
    case 'NotebookEdit':
    case 'edit':
    case 'write':
      return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        editDiffView(item, t),
        // Edit results echo the changed file region; wrap so long lines stay
        // readable without horizontal scrolling.
        _resultSection(item, t, wrap: true),
      ]);
    case 'Bash':
    case 'bash':
    case 'shell':
      return _bash(item, t);
    case 'execute':
      return _execute(item, t);
    case 'Read':
    case 'NotebookRead':
    case 'read':
      return _read(item, t);
    case 'Grep':
    case 'grep':
      return _grep(item, t);
    case 'Glob':
    case 'LS':
    case 'glob':
      return _glob(item, t);
    case 'WebFetch':
    case 'WebSearch':
    case 'webfetch':
    case 'websearch':
      return _web(item, t);
    case 'TodoWrite':
    case 'todowrite':
      return _todo(item, t);
    case 'AskUserQuestion':
    case 'question':
      return _askUserQuestion(item, t);
    case 'EnterPlanMode':
      return toolGeneric(item, t, resultLang: 'markdown');
    case 'ExitPlanMode':
      return _exitPlanMode(item, t);
    case 'Skill':
    case 'skill':
      return _skill(item, t);
    default:
      return toolGeneric(item, t);
  }
}

// ExitPlanMode input carries a markdown `plan` and its `planFilePath`; the result
// is the approval/rejection text. Render the plan (and result) as real markdown
// rather than raw JSON / a highlighted code box.
Widget _exitPlanMode(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  final plan = toolInputStr(m['plan']);
  final planFilePath = toolInputStr(m['planFilePath']);
  final result = it.result ?? '';
  final hasStructured = plan.isNotEmpty || planFilePath.isNotEmpty;
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (planFilePath.isNotEmpty) ...[
      toolLabel('Plan file', t),
      SelectableText(planFilePath,
          style: codeMono.copyWith(color: t.textSecondary)),
    ],
    if (plan.isNotEmpty) ...[
      toolLabel('Plan', t),
      _CollapsibleMarkdown(plan),
    ],
    // Unknown/empty input shape (e.g. `{}`): fall back so nothing is dropped.
    if (!hasStructured && (it.toolInput ?? '').isNotEmpty) ...[
      toolLabel('Input', t),
      codeBlock(it.toolInput!),
    ],
    if (result.isNotEmpty)
      toolResult(it, appMarkdown(result), t),
  ]);
}

/// Renders [data] as markdown, collapsed to a clipped preview by default when
/// long (matching the app's line/char-count collapse heuristic), with a
/// Show more / Show less toggle.
class _CollapsibleMarkdown extends StatefulWidget {
  const _CollapsibleMarkdown(this.data);
  final String data;

  @override
  State<_CollapsibleMarkdown> createState() => _CollapsibleMarkdownState();
}

class _CollapsibleMarkdownState extends State<_CollapsibleMarkdown> {
  bool _expanded = false;
  static const _collapsedHeight = 220.0;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final md = appMarkdown(widget.data);
    final lines = '\n'.allMatches(widget.data).length + 1;
    final long = lines > 12 || widget.data.length > 600;
    if (!long) return md;

    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      if (_expanded)
        md
      else
        // Clip the rendered markdown to a preview height: OverflowBox lets it lay
        // out at its natural height (bounded width, so prose still wraps) while
        // ClipRect trims everything past _collapsedHeight.
        ClipRect(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxHeight: _collapsedHeight),
            child: OverflowBox(
              alignment: Alignment.topLeft,
              maxHeight: double.infinity,
              child: md,
            ),
          ),
        ),
      GestureDetector(
        key: const Key('plan-toggle'),
        onTap: () => setState(() => _expanded = !_expanded),
        child: Padding(
          padding: const EdgeInsets.only(top: 6),
          child: Text(_expanded ? 'Show less' : 'Show more',
              style: TextStyle(
                  color: t.accent,
                  fontWeight: FontWeight.w700,
                  fontSize: 13)),
        ),
      ),
    ]);
  }
}

Widget _skill(Entry it, ArgusTokens t) {
  // Claude uses `skill`; opencode uses `id`.
  final m = toolInput(it);
  final name = toolInputStr(m['skill'] ?? m['id']);
  final result = it.result ?? '';
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    toolLabel('Skill', t),
    if (name.isNotEmpty)
      toolHeader(name, t)
    else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
    if (result.isNotEmpty)
      // The loaded skill body is markdown — render it, don't show the source.
      toolResult(it, appMarkdown(result), t),
  ]);
}

Widget _bash(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  final desc = toolInputStr(m['description']), cmd = toolInputStr(m['command']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (desc.isNotEmpty)
      toolComment(desc, t),
    // Commands are frequently multi-line (heredocs, `&&` chains) — a bash code
    // block gives highlighting, wrap, and copy for free.
    if (cmd.isNotEmpty)
      codeBlock(cmd, lang: 'bash')
    else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
    _resultSection(it, t),
  ]);
}

// opencode's `execute` runs code, not a shell command (unlike `bash`/`shell`),
// so its input is under `code`.
Widget _execute(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  final code = toolInputStr(m['code']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (code.isNotEmpty)
      codeBlock(code, lang: 'javascript')
    else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
    _resultSection(it, t),
  ]);
}

Widget _read(Entry it, ArgusTokens t) {
  // Claude uses `file_path`; opencode uses `path`.
  final m = toolInput(it);
  final path = toolInputStr(m['file_path'] ?? m['path']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (path.isNotEmpty) toolHeader(path, t),
    // Prefer the file extension; fall back to auto-detection when unknown.
    // Read output is already `cat -n` numbered — no need for our own gutter.
    if ((it.result ?? '').isNotEmpty)
      codeBlock(it.result!,
          lang: langFromPath(path), lineNumberToggle: false),
  ]);
}

Widget _grep(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  // Claude uses `glob`; opencode uses `include`.
  var scope = toolInputStr(m['glob'] ?? m['include']);
  final path = toolInputStr(m['path']);
  if (path.isNotEmpty) scope = scope.isEmpty ? path : '$scope $path';
  final head = '"${toolInputStr(m['pattern'])}"${scope.isEmpty ? '' : ' in $scope'}';
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    toolHeader(head, t),
    if ((it.result ?? '').isNotEmpty) codeBlock(it.result!),
  ]);
}

Widget _glob(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  final head = toolInputStr(m['pattern']).isNotEmpty ? toolInputStr(m['pattern']) : toolInputStr(m['path']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (head.isNotEmpty) toolHeader(head, t),
    if ((it.result ?? '').isNotEmpty) codeBlock(it.result!),
  ]);
}

Widget _web(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  final url = toolInputStr(m['url']);
  final query = toolInputStr(m['query']);
  final prompt = toolInputStr(m['prompt']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (url.isNotEmpty) ...[toolLabel('URL', t), toolHeader(url, t)],
    if (query.isNotEmpty) ...[toolLabel('Query', t), toolHeader(query, t)],
    if (prompt.isNotEmpty) ...[
      toolLabel('Prompt', t),
      Text(prompt, style: codeMono.copyWith(color: t.textDim)),
    ],
    if ((it.result ?? '').isNotEmpty)
      // Web results come back as markdown — render them, don't show the source.
      toolResult(it, appMarkdown(it.result!), t),
  ]);
}

Widget _todo(Entry it, ArgusTokens t) {
  final todos = (toolInput(it)['todos'] as List?) ?? const [];
  if (todos.isEmpty) return toolGeneric(it, t);
  final rows = <Widget>[];
  for (final todo in todos.cast<Map<String, dynamic>>()) {
    final status = toolInputStr(todo['status']);
    var glyph = '☐', text = toolInputStr(todo['content']);
    if (status == 'completed') {
      glyph = '☑';
    } else if (status == 'in_progress') {
      glyph = '◐';
      if (toolInputStr(todo['activeForm']).isNotEmpty) text = toolInputStr(todo['activeForm']);
    }
    rows.add(Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Text('$glyph $text', style: codeMono.copyWith(color: t.text)),
    ));
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: rows);
}

/// Parses an AskUserQuestion result (`"question"="answer", ...`) into a
/// question→answer map. Questions and answers may contain quotes, so each answer
/// is anchored on its known question: it runs to the next question's key, or to
/// the last quote.
Map<String, String> answeredAnswers(String result, List<String> questions) {
  final keys = <({String question, int start, int end})>[];
  for (final q in questions) {
    if (q.isEmpty) continue;
    final k = '"$q"="';
    final i = result.indexOf(k);
    if (i >= 0) keys.add((question: q, start: i, end: i + k.length));
  }
  keys.sort((a, b) => a.start.compareTo(b.start));
  final out = <String, String>{};
  for (var i = 0; i < keys.length; i++) {
    final k = keys[i];
    if (i + 1 < keys.length) {
      var v = result.substring(k.end, keys[i + 1].start);
      if (v.endsWith('", ')) v = v.substring(0, v.length - 3);
      out[k.question] = v;
    } else {
      final j = result.lastIndexOf('"');
      if (j >= k.end) out[k.question] = result.substring(k.end, j);
    }
  }
  return out;
}

Widget _askUserQuestion(Entry it, ArgusTokens t) {
  final qs = (toolInput(it)['questions'] as List?) ?? const [];
  final answers = answeredAnswers(it.result ?? '', [
    for (final q in qs.whereType<Map<String, dynamic>>())
      toolInputStr(q['question']),
  ]);
  return questionsDetail(
      it,
      (q) => (
            picks: (answers[toolInputStr(q['question'])] ?? '').split(', '),
            note: '',
          ), t);
}

/// Renders a questions tool's input. [answer] returns a question's picked
/// labels, where picks matching no option are shown as custom answers, and an
/// optional note.
Widget questionsDetail(
    Entry it,
    ({List<String> picks, String note}) Function(Map<String, dynamic> q)
        answer, ArgusTokens t) {
  final qs = (toolInput(it)['questions'] as List?) ?? const [];
  if (qs.isEmpty) return toolGeneric(it, t);
  final blocks = <Widget>[];
  for (final q in qs.cast<Map<String, dynamic>>()) {
    final question = toolInputStr(q['question']);
    final multi = (q['multiSelect'] as bool?) ?? false;
    final a = answer(q);
    final chosen =
        a.picks.map((s) => s.trim()).where((s) => s.isNotEmpty).toSet();
    final children = <Widget>[];
    if (toolInputStr(q['header']).isNotEmpty) {
      children.add(Text(toolInputStr(q['header']).toUpperCase(),
          style: codeMono.copyWith(
              color: t.accent, fontWeight: FontWeight.w700)));
    }
    if (question.isNotEmpty) children.add(appMarkdown(question));
    for (final opt
        in ((q['options'] as List?) ?? const []).cast<Map<String, dynamic>>()) {
      final label = toolInputStr(opt['label']);
      final isChosen = chosen.remove(label);
      final mark =
          multi ? (isChosen ? '[x]' : '[ ]') : (isChosen ? '◉' : '○');
      children.add(Padding(
        padding: const EdgeInsets.only(top: 6),
        child: Text('$mark $label',
            style: TextStyle(
                color: isChosen ? t.text : t.textSecondary,
                fontWeight: isChosen ? FontWeight.w700 : FontWeight.w400)),
      ));
      if (toolInputStr(opt['description']).isNotEmpty) {
        children.add(Padding(
          padding: const EdgeInsets.only(left: 20, top: 2),
          child: Text(toolInputStr(opt['description']),
              style: TextStyle(color: t.textDim, fontSize: 13)),
        ));
      }
      if (toolInputStr(opt['preview']).isNotEmpty) {
        children.add(Padding(
          padding: const EdgeInsets.only(left: 20, top: 2),
          child: codeBlock(toolInputStr(opt['preview'])),
        ));
      }
    }
    for (final custom in chosen) {
      children.add(Padding(
        padding: const EdgeInsets.only(top: 6),
        child: Text('Answer: $custom',
            style: TextStyle(
                color: t.textSecondary, fontWeight: FontWeight.w700)),
      ));
    }
    if (a.note.isNotEmpty) {
      children.add(Padding(
        padding: const EdgeInsets.only(top: 6),
        child: Text('Note: ${a.note}',
            style: TextStyle(
                color: t.textSecondary, fontWeight: FontWeight.w700)),
      ));
    }
    blocks.add(Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Column(
          crossAxisAlignment: CrossAxisAlignment.start, children: children),
    ));
  }
  // The agent rejected the call (e.g. malformed arguments): say why.
  if (it.resultIsError) blocks.add(_resultSection(it, t, wrap: true));
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: blocks);
}

/// Renders an MCP tool name `mcp__<server>__<tool>` as "server › tool",
/// splitting at the first `__` after the prefix; null for other names.
String? mcpDisplayName(String name) {
  if (!name.startsWith('mcp__')) return null;
  final rest = name.substring(5);
  final idx = rest.indexOf('__');
  if (idx <= 0 || idx + 2 >= rest.length) return null;
  return '${rest.substring(0, idx)} › ${rest.substring(idx + 2)}';
}

/// Renders an MCP tool call's top-level arguments as key/value lines.
Widget mcpDetail(Entry it, ArgusTokens t) {
  final args = toolInput(it);
  final keys = args.keys.toList()..sort();
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (keys.isNotEmpty)
      for (final k in keys)
        toolHeadline('$k: ',
            args[k] is String ? args[k] as String : jsonEncode(args[k]), t)
    else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
    _resultSection(it, t),
  ]);
}
