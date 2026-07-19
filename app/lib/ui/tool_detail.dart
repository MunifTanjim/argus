import 'dart:convert';

import 'package:flutter/material.dart';

import '../models/chunk.dart';
import 'code_block.dart';
import 'edit_diff.dart';
import 'theme.dart';
import 'tool_registry.dart';

const _red = Color(0xFFfb4934);
const _mono = TextStyle(fontFamily: 'monospace', fontSize: 12, height: 1.35);

Map<String, dynamic> _input(Item it) {
  try {
    return jsonDecode(it.toolInput ?? '') as Map<String, dynamic>;
  } catch (_) {
    return const {};
  }
}

Widget _label(String text, {bool error = false}) => Padding(
      padding: const EdgeInsets.only(top: 8, bottom: 4),
      child: Text(text,
          style: TextStyle(
              color: error ? _red : AppColors.secondary,
              fontWeight: FontWeight.w700,
              fontSize: 13)),
    );

Widget _header(String text) => Text(text,
    style:
        _mono.copyWith(color: AppColors.secondary, fontWeight: FontWeight.w700));

Widget _resultSection(Item it, {bool wrap = false, String? lang}) {
  if ((it.result ?? '').isEmpty) return const SizedBox.shrink();
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    _label(it.resultIsError ? 'Error' : 'Result', error: it.resultIsError),
    codeBlock(it.result!, wrap: wrap, lang: lang),
  ]);
}

Widget toolDetailBody(Item item) {
  if (item.kind == ItemKind.thinking) {
    return appMarkdown(item.text ?? '');
  }
  final detail = toolMeta(item.toolName)?.detail;
  if (detail != null) return detail(item);
  switch (item.toolName) {
    case 'Edit':
    case 'MultiEdit':
    case 'Write':
    case 'NotebookEdit':
      return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        editDiffView(item),
        // Edit results echo the changed file region; wrap so long lines stay
        // readable without horizontal scrolling.
        _resultSection(item, wrap: true),
      ]);
    case 'Bash':
      return _bash(item);
    case 'Read':
    case 'NotebookRead':
      return _read(item);
    case 'Grep':
      return _grep(item);
    case 'Glob':
    case 'LS':
      return _glob(item);
    case 'WebFetch':
    case 'WebSearch':
      return _web(item);
    case 'TodoWrite':
      return _todo(item);
    case 'AskUserQuestion':
      return _askUserQuestion(item);
    case 'EnterPlanMode':
      return _generic(item, resultLang: 'markdown');
    case 'ExitPlanMode':
      return _exitPlanMode(item);
    case 'Skill':
      return _skill(item);
    default:
      return _generic(item);
  }
}

Widget _generic(Item it, {String? resultLang}) =>
    Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      if ((it.toolInput ?? '').isNotEmpty) ...[
        _label('Input'),
        codeBlock(it.toolInput!),
      ],
      _resultSection(it, lang: resultLang),
    ]);

// ExitPlanMode input carries a markdown `plan` and its `planFilePath`; the result
// is the approval/rejection text. Render the plan (and result) as real markdown
// rather than raw JSON / a highlighted code box.
Widget _exitPlanMode(Item it) {
  final m = _input(it);
  final plan = toolInputStr(m['plan']);
  final planFilePath = toolInputStr(m['planFilePath']);
  final result = it.result ?? '';
  final hasStructured = plan.isNotEmpty || planFilePath.isNotEmpty;
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (planFilePath.isNotEmpty) ...[
      _label('Plan file'),
      SelectableText(planFilePath,
          style: _mono.copyWith(color: AppColors.secondary)),
    ],
    if (plan.isNotEmpty) ...[
      _label('Plan'),
      _CollapsibleMarkdown(plan),
    ],
    // Unknown/empty input shape (e.g. `{}`): fall back so nothing is dropped.
    if (!hasStructured && (it.toolInput ?? '').isNotEmpty) ...[
      _label('Input'),
      codeBlock(it.toolInput!),
    ],
    if (result.isNotEmpty) ...[
      _label(it.resultIsError ? 'Error' : 'Result', error: it.resultIsError),
      appMarkdown(result),
    ],
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
              style: const TextStyle(
                  color: AppColors.accent,
                  fontWeight: FontWeight.w700,
                  fontSize: 13)),
        ),
      ),
    ]);
  }
}

Widget _skill(Item it) {
  final name = toolInputStr(_input(it)['skill']);
  final result = it.result ?? '';
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    _label('Skill'),
    if (name.isNotEmpty)
      _header(name)
    else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
    if (result.isNotEmpty) ...[
      _label(it.resultIsError ? 'Error' : 'Result', error: it.resultIsError),
      // The loaded skill body is markdown — render it, don't show the source.
      appMarkdown(result),
    ],
  ]);
}

Widget _bash(Item it) {
  final m = _input(it);
  final desc = toolInputStr(m['description']), cmd = toolInputStr(m['command']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (desc.isNotEmpty)
      Text('# $desc', style: _mono.copyWith(color: AppColors.dim)),
    // Commands are frequently multi-line (heredocs, `&&` chains) — a bash code
    // block gives highlighting, wrap, and copy for free.
    if (cmd.isNotEmpty)
      codeBlock(cmd, lang: 'bash')
    else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
    _resultSection(it),
  ]);
}

Widget _read(Item it) {
  final path = toolInputStr(_input(it)['file_path']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (path.isNotEmpty) _header(path),
    // Prefer the file extension; fall back to auto-detection when unknown.
    // Read output is already `cat -n` numbered — no need for our own gutter.
    if ((it.result ?? '').isNotEmpty)
      codeBlock(it.result!,
          lang: langFromPath(path), lineNumberToggle: false),
  ]);
}

Widget _grep(Item it) {
  final m = _input(it);
  var scope = toolInputStr(m['glob']);
  final path = toolInputStr(m['path']);
  if (path.isNotEmpty) scope = scope.isEmpty ? path : '$scope $path';
  final head = '"${toolInputStr(m['pattern'])}"${scope.isEmpty ? '' : ' in $scope'}';
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    _header(head),
    if ((it.result ?? '').isNotEmpty) codeBlock(it.result!),
  ]);
}

Widget _glob(Item it) {
  final m = _input(it);
  final head = toolInputStr(m['pattern']).isNotEmpty ? toolInputStr(m['pattern']) : toolInputStr(m['path']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (head.isNotEmpty) _header(head),
    if ((it.result ?? '').isNotEmpty) codeBlock(it.result!),
  ]);
}

Widget _web(Item it) {
  final m = _input(it);
  final url = toolInputStr(m['url']);
  final query = toolInputStr(m['query']);
  final prompt = toolInputStr(m['prompt']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (url.isNotEmpty) ...[_label('URL'), _header(url)],
    if (query.isNotEmpty) ...[_label('Query'), _header(query)],
    if (prompt.isNotEmpty) ...[
      _label('Prompt'),
      Text(prompt, style: _mono.copyWith(color: AppColors.dim)),
    ],
    if ((it.result ?? '').isNotEmpty) ...[
      _label(it.resultIsError ? 'Error' : 'Result', error: it.resultIsError),
      // Web results come back as markdown — render them, don't show the source.
      appMarkdown(it.result!),
    ],
  ]);
}

Widget _todo(Item it) {
  final todos = (_input(it)['todos'] as List?) ?? const [];
  if (todos.isEmpty) return _generic(it);
  final rows = <Widget>[];
  for (final t in todos.cast<Map<String, dynamic>>()) {
    final status = toolInputStr(t['status']);
    var glyph = '☐', text = toolInputStr(t['content']);
    if (status == 'completed') {
      glyph = '☑';
    } else if (status == 'in_progress') {
      glyph = '◐';
      if (toolInputStr(t['activeForm']).isNotEmpty) text = toolInputStr(t['activeForm']);
    }
    rows.add(Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Text('$glyph $text', style: _mono.copyWith(color: AppColors.text)),
    ));
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: rows);
}

final _answerPair = RegExp(r'"([^"]+)"="([^"]*)"');

String? answeredAnswer(String result, String question) {
  for (final m in _answerPair.allMatches(result)) {
    if (m.group(1) == question) return m.group(2);
  }
  return null;
}

Widget _askUserQuestion(Item it) {
  final qs = (_input(it)['questions'] as List?) ?? const [];
  if (qs.isEmpty) return _generic(it);
  final result = it.result ?? '';
  final blocks = <Widget>[];
  for (final q in qs.cast<Map<String, dynamic>>()) {
    final question = toolInputStr(q['question']);
    final multi = (q['multiSelect'] as bool?) ?? false;
    final chosen = (answeredAnswer(result, question) ?? '')
        .split(', ')
        .map((s) => s.trim())
        .where((s) => s.isNotEmpty)
        .toSet();
    final children = <Widget>[];
    if (toolInputStr(q['header']).isNotEmpty) {
      children.add(Text(toolInputStr(q['header']).toUpperCase(),
          style: _mono.copyWith(
              color: AppColors.accent, fontWeight: FontWeight.w700)));
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
                color: isChosen ? AppColors.text : AppColors.secondary,
                fontWeight: isChosen ? FontWeight.w700 : FontWeight.w400)),
      ));
      if (toolInputStr(opt['description']).isNotEmpty) {
        children.add(Padding(
          padding: const EdgeInsets.only(left: 20, top: 2),
          child: Text(toolInputStr(opt['description']),
              style: const TextStyle(color: AppColors.dim, fontSize: 13)),
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
            style: const TextStyle(
                color: AppColors.secondary, fontWeight: FontWeight.w700)),
      ));
    }
    blocks.add(Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Column(
          crossAxisAlignment: CrossAxisAlignment.start, children: children),
    ));
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: blocks);
}
