import 'dart:convert';

import 'package:flutter/material.dart';

import '../models/entry.dart';
import 'code_block.dart';
import 'edit_diff.dart';
import 'theme.dart';
import 'tool_detail_common.dart';

/// [head], then the result under its label when there is one.
Widget _withResult(List<Widget> head, Entry it, Widget? body, ArgusTokens t) =>
    Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [...head, toolResult(it, body, t)],
    );

String agyResultBody(String result) {
  final lines = result.split('\n');
  var i = 0;
  while (i < lines.length) {
    final t = lines[i].trim();
    if (t.startsWith('Created At:') || t.startsWith('Completed At:')) {
      i++;
      continue;
    }
    break;
  }
  return lines.sublist(i).join('\n').replaceFirst(RegExp(r'^\n+'), '');
}

const _agyBoilerplate = [
  "If relevant, proactively run terminal commands to execute this code for the USER. Don't ask for permission.",
  "Do not output the path of this image to show to the user since the user can already see it. However, you can embed this image in artifacts for the USER's review.",
];

/// Removes recurring instruction sentences agy appends to results.
String stripAgyBoilerplate(String s) {
  for (final b in _agyBoilerplate) {
    s = s.replaceAll(b, '');
  }
  return s.trim();
}

String stripAgyReminder(String s) {
  final i = s.indexOf('REMINDER:');
  if (i >= 0) s = s.substring(0, i);
  return s.trim();
}

String formatBytes(int n) {
  if (n >= 1 << 20) return '${(n / (1 << 20)).toStringAsFixed(1)}M';
  if (n >= 1 << 10) return '${(n / (1 << 10)).toStringAsFixed(1)}k';
  return '${n}B';
}

String _stripLinePrefix(String s, String prefix) =>
    s.split('\n').map((l) {
      var out = l;
      while (out.startsWith(prefix)) {
        out = out.substring(prefix.length);
      }
      return out;
    }).join('\n');

/// A run_command result split at its output marker (Output:/Stdout:/Stderr:).
class RunCommandResult {
  const RunCommandResult(this.head, this.output, this.hasMarker);
  final String head;
  final String output;
  final bool hasMarker;
}

RunCommandResult splitRunCommandResult(String result) {
  final lines =
      result.replaceFirst(RegExp(r'\n+$'), '').split('\n');
  var outIdx = -1;
  var indent = '';
  for (var i = 0; i < lines.length; i++) {
    final trimmed = lines[i].replaceFirst(RegExp(r'^\t+'), '');
    if (trimmed == 'Output:' || trimmed == 'Stdout:' || trimmed == 'Stderr:') {
      outIdx = i;
      indent = lines[i].substring(0, lines[i].length - trimmed.length);
      break;
    }
  }
  if (outIdx < 0) {
    return RunCommandResult(_stripLinePrefix(result, '\t'), '', false);
  }
  String strip(Iterable<String> ls) =>
      ls.map((l) => l.startsWith(indent) ? l.substring(indent.length) : l).join('\n');
  final head = strip(lines.sublist(0, outIdx + 1));
  final output = outIdx + 1 < lines.length
      ? strip(lines.sublist(outIdx + 1)).replaceFirst(RegExp(r'\n+$'), '')
      : '';
  return RunCommandResult(head, output, true);
}

List<String> grepRows(String body) {
  final rows = <String>[];
  for (var ln in body.split('\n')) {
    ln = ln.trim();
    if (ln.isEmpty) continue;
    Map<String, dynamic>? e;
    try {
      e = jsonDecode(ln) as Map<String, dynamic>;
    } catch (_) {
      e = null;
    }
    if (e != null && (e['File'] as String? ?? '').isNotEmpty) {
      rows.add(
          '${e['File']}:${e['LineNumber'] ?? 0} ${(e['LineContent'] as String? ?? '').trim()}');
    } else {
      rows.add(ln);
    }
  }
  return rows;
}

List<String> listDirRows(String body) {
  final rows = <String>[];
  for (var ln in body.split('\n')) {
    ln = ln.trim();
    if (ln.isEmpty) continue;
    Map<String, dynamic>? e;
    try {
      e = jsonDecode(ln) as Map<String, dynamic>;
    } catch (_) {
      e = null;
    }
    final name = e?['name'] as String? ?? '';
    if (e != null && name.isNotEmpty) {
      if (e['isDir'] as bool? ?? false) {
        rows.add('$name/');
        continue;
      }
      final size = int.tryParse(e['sizeBytes'] as String? ?? '');
      rows.add(size != null ? '$name  ${formatBytes(size)}' : name);
    } else {
      rows.add(ln);
    }
  }
  return rows;
}

({String meta, String content}) splitViewFileResult(String result) {
  final body = agyResultBody(result);
  if (body.isEmpty) return (meta: '', content: '');
  final lines = body.split('\n');
  final metaParts = <String>[];
  var contentStart = -1;
  for (var i = 0; i < lines.length; i++) {
    final t = lines[i].trim();
    if (t.startsWith('Total Lines:') ||
        t.startsWith('Total Bytes:') ||
        t.startsWith('Showing lines')) {
      metaParts.add(t);
    } else if (t.startsWith('The following code')) {
      contentStart = i + 1;
    }
    if (contentStart >= 0) break;
  }
  final content = (contentStart >= 0 && contentStart < lines.length)
      ? lines.sublist(contentStart).join('\n').replaceFirst(RegExp(r'\n+$'), '')
      : '';
  return (meta: metaParts.join(' · '), content: content);
}

/// Parses agy's "A1: …\nA2: …" answer block into a 0-based index map.
Map<int, String> parseAgyAnswers(String result) {
  final out = <int, String>{};
  for (var ln in result.split('\n')) {
    ln = ln.trim();
    if (ln.length < 3 || ln[0] != 'A') continue;
    final colon = ln.indexOf(':');
    if (colon < 0) continue;
    final n = int.tryParse(ln.substring(1, colon).trim());
    if (n == null) continue;
    out[n - 1] = ln.substring(colon + 1).trim();
  }
  return out;
}

String agyImageResult(String result) {
  final body = stripAgyBoilerplate(agyResultBody(result));
  return body
      .split('\n')
      .map((l) => l.trim())
      .where((t) => t.isNotEmpty && !t.startsWith('Using prompt:'))
      .join('\n');
}

Widget agyRunCommandDetail(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  final cmd = toolInputStr(m['CommandLine']);
  final cwd = toolInputStr(m['Cwd']);
  final head = <Widget>[
    if (cwd.isNotEmpty) toolComment(cwd, t),
    if (cmd.isNotEmpty)
      codeBlock(cmd, lang: 'bash')
    else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
  ];
  Widget? body;
  if ((it.result ?? '').isNotEmpty) {
    final r = splitRunCommandResult(it.result!);
    body = Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      if (r.head.trim().isNotEmpty) kvDump(r.head, t),
      if (r.output.isNotEmpty) codeBlock(r.output),
      if (!r.hasMarker && r.head.trim().isEmpty) codeBlock(it.result!),
    ]);
  }
  return _withResult(head, it, body, t);
}

Widget agyGrepSearchDetail(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  final query = toolInputStr(m['Query']);
  final path = toolInputStr(m['SearchPath']);
  final flags = [
    if (m['IsRegex'] as bool? ?? false) 'regex',
    if (m['CaseInsensitive'] as bool? ?? false) 'case-insensitive',
  ];
  final head = <Widget>[
    if (query.isNotEmpty)
      RichText(
        text: TextSpan(children: [
          TextSpan(
              text: '"$query"',
              style: toolHeaderStyle(t)),
          if (path.isNotEmpty)
            TextSpan(
                text: ' in $path', style: codeMono.copyWith(color: t.textDim)),
          if (flags.isNotEmpty)
            TextSpan(
                text: '  (${flags.join(' · ')})',
                style: codeMono.copyWith(color: t.textDim)),
        ]),
      ),
  ];
  final rows = grepRows(agyResultBody(it.result ?? ''));
  return _withResult(
      head, it, rows.isEmpty ? null : codeBlock(rows.join('\n')), t);
}

Widget agyListDirDetail(Entry it, ArgusTokens t) {
  final path = toolInputStr(toolInput(it)['DirectoryPath']);
  final rows = listDirRows(agyResultBody(it.result ?? ''));
  return _withResult(
    [if (path.isNotEmpty) toolHeader(path, t)],
    it,
    rows.isEmpty ? null : codeBlock(rows.join('\n')), t,
  );
}

Widget agyViewFileDetail(Entry it, ArgusTokens t) {
  final path = toolInputStr(toolInput(it)['AbsolutePath']);
  final (:meta, :content) = splitViewFileResult(it.result ?? '');
  Widget? body;
  if (content.isNotEmpty) {
    body = codeBlock(content, lineNumberToggle: false);
  } else if ((it.result ?? '').isNotEmpty) {
    body = codeBlock(agyResultBody(it.result!));
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (path.isNotEmpty) toolHeader(path, t),
    if (meta.isNotEmpty)
      Padding(
        padding: const EdgeInsets.only(top: 2),
        child: Text(meta, style: codeMono.copyWith(color: t.textDim)),
      ),
    toolResult(it, body, t),
  ]);
}

Widget agyWriteToFileDetail(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  final path = toolInputStr(m['TargetFile']);
  final desc = toolInputStr(m['Description']);
  final code = toolInputStr(m['CodeContent']);
  final overwrite = m['Overwrite'] as bool? ?? false;
  final head = <Widget>[
    if (desc.isNotEmpty) toolComment(desc, t),
    if (path.isNotEmpty)
      Text('● $path${overwrite ? '  (overwrite)' : ''}',
          style: codeMono.copyWith(color: t.textDim)),
    if (code.isNotEmpty)
      diffView('', code, t, lang: path)
    else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
  ];
  final body = (it.result ?? '').isNotEmpty
      ? appMarkdown(stripAgyBoilerplate(agyResultBody(it.result!)))
      : null;
  return _withResult(head, it, body, t);
}

Widget agyReplaceFileContentDetail(Entry it, ArgusTokens t) => _replaceDetail(it, t, multi: false);
Widget agyMultiReplaceFileContentDetail(Entry it, ArgusTokens t) =>
    _replaceDetail(it, t, multi: true);

Widget _replaceDetail(Entry it, ArgusTokens t, {required bool multi}) {
  final m = toolInput(it);
  final path = toolInputStr(m['TargetFile']);
  final desc = toolInputStr(m['Description']);
  final children = <Widget>[
    if (desc.isNotEmpty) toolComment(desc, t),
  ];
  if (multi) {
    final chunks = (m['ReplacementChunks'] as List?) ?? const [];
    if (path.isNotEmpty) {
      children.add(Text('● $path${chunks.isEmpty ? '' : '  (${chunks.length} edits)'}',
          style: codeMono.copyWith(color: t.textDim)));
    }
    for (var i = 0; i < chunks.length; i++) {
      final c = chunks[i] as Map<String, dynamic>;
      children.add(Padding(
        padding: const EdgeInsets.only(top: 6, bottom: 2),
        child: Text(
            '─── edit ${i + 1} (lines ${c['StartLine'] ?? 0}–${c['EndLine'] ?? 0}) ───',
            style: codeMono.copyWith(color: t.textDim)),
      ));
      children.add(diffView(toolInputStr(c['TargetContent']),
          toolInputStr(c['ReplacementContent']), t,
          lang: path));
    }
    if (chunks.isEmpty && (it.toolInput ?? '').isNotEmpty) {
      children.add(codeBlock(it.toolInput!));
    }
  } else {
    final start = (m['StartLine'] as num?)?.toInt() ?? 0;
    final end = (m['EndLine'] as num?)?.toInt() ?? 0;
    if (path.isNotEmpty) {
      children.add(Text('● $path${start > 0 || end > 0 ? '  (lines $start–$end)' : ''}',
          style: codeMono.copyWith(color: t.textDim)));
    }
    final target = toolInputStr(m['TargetContent']);
    final replacement = toolInputStr(m['ReplacementContent']);
    if (target.isNotEmpty || replacement.isNotEmpty) {
      children.add(diffView(target, replacement, t, lang: path));
    } else if ((it.toolInput ?? '').isNotEmpty) {
      children.add(codeBlock(it.toolInput!));
    }
  }
  // The result just echoes the diff, so show it only on error.
  return _withResult(
    children,
    it,
    it.resultIsError && (it.result ?? '').isNotEmpty
        ? codeBlock(agyResultBody(it.result!))
        : null, t,
  );
}

Widget agySearchWebDetail(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  final query = toolInputStr(m['query']);
  final domain = toolInputStr(m['domain']);
  var body = agyResultBody(it.result ?? '');
  const lead = 'returned the following summary:';
  final idx = body.indexOf(lead);
  if (idx >= 0) body = body.substring(idx + lead.length).trim();
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (query.isNotEmpty)
      RichText(
        text: TextSpan(children: [
          TextSpan(
              text: '"$query"',
              style: toolHeaderStyle(t)),
          if (domain.isNotEmpty)
            TextSpan(
                text: '  $domain', style: codeMono.copyWith(color: t.textDim)),
        ]),
      ),
    if (body.isNotEmpty) ...[
      toolLabel('Result', t),
      appMarkdown(body),
    ],
  ]);
}

Widget agyGenerateImageDetail(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  final name = toolInputStr(m['ImageName']);
  final ratio = toolInputStr(m['AspectRatio']);
  final prompt = toolInputStr(m['Prompt']);
  final head = <Widget>[
    if (name.isNotEmpty)
      toolHeader('$name${ratio.isNotEmpty ? '  ($ratio)' : ''}', t),
    if (prompt.isNotEmpty) toolComment(prompt, t),
  ];
  final img = agyImageResult(it.result ?? '');
  return _withResult(head, it, img.isEmpty ? null : codeBlock(img), t);
}

Widget agyDefineSubagentDetail(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  final name = toolInputStr(m['name']);
  final desc = toolInputStr(m['description']);
  final prompt = toolInputStr(m['system_prompt']);
  final tools = [
    if (m['enable_write_tools'] as bool? ?? false) 'write',
    if (m['enable_mcp_tools'] as bool? ?? false) 'mcp',
    if (m['enable_subagent_tools'] as bool? ?? false) 'subagent',
  ];
  final children = <Widget>[
    if (name.isNotEmpty)
      toolHeader(name, t),
    if (desc.isNotEmpty)
      Padding(
        padding: const EdgeInsets.only(top: 2),
        child: Text(desc, style: codeMono.copyWith(color: t.textDim)),
      ),
    Padding(
      padding: const EdgeInsets.only(top: 2),
      child: Text('tools: ${tools.isEmpty ? 'none' : tools.join(' · ')}',
          style: codeMono.copyWith(color: t.textDim)),
    ),
    if (prompt.isNotEmpty) ...[
      toolLabel('System Prompt', t),
      appMarkdown(prompt),
    ],
  ];
  return _withResult(
    children,
    it,
    it.resultIsError && (it.result ?? '').isNotEmpty
        ? codeBlock(agyResultBody(it.result!))
        : null, t,
  );
}

Widget agyManageSubagentsDetail(Entry it, ArgusTokens t) {
  final action = toolInputStr(toolInput(it)['Action']);
  final body = agyResultBody(it.result ?? '');
  return _withResult(
    [
      if (action.isNotEmpty)
        toolHeader(action, t),
    ],
    it,
    body.isEmpty ? null : appMarkdown(body), t,
  );
}

Widget agyManageTaskDetail(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  final action = toolInputStr(m['Action']);
  final taskId = toolInputStr(m['TaskId']);
  final body = stripAgyReminder(agyResultBody(it.result ?? ''));
  return _withResult(
    [
      if (action.isNotEmpty)
        RichText(
          text: TextSpan(children: [
            TextSpan(
                text: action,
                style: toolHeaderStyle(t)),
            if (taskId.isNotEmpty)
              TextSpan(
                  text: '  $taskId',
                  style: codeMono.copyWith(color: t.textDim)),
          ]),
        ),
    ],
    it,
    body.isEmpty ? null : kvDump(body, t), t,
  );
}

Widget agyAskQuestionDetail(Entry it, ArgusTokens t) {
  final qs = (toolInput(it)['questions'] as List?) ?? const [];
  if (qs.isEmpty) return _generic(it, t);
  final answers = parseAgyAnswers(agyResultBody(it.result ?? ''));
  final blocks = <Widget>[];
  for (var qi = 0; qi < qs.length; qi++) {
    final q = qs[qi] as Map<String, dynamic>;
    final question = toolInputStr(q['question']);
    final multi = q['is_multi_select'] as bool? ?? false;
    final ans = answers[qi] ?? '';
    final children = <Widget>[
      Row(children: [
        Icon(Icons.chat_bubble_outline, size: 14, color: t.accent),
        const SizedBox(width: 6),
        Text('Agent is asking',
            style: codeMono.copyWith(
                color: t.accent, fontWeight: FontWeight.w700)),
      ]),
    ];
    if (question.isNotEmpty) children.add(appMarkdown(question));
    for (final opt in ((q['options'] as List?) ?? const []).cast<String>()) {
      final isChosen = ans.isNotEmpty && ans.contains(opt);
      final mark = multi ? (isChosen ? '[x]' : '[ ]') : (isChosen ? '◉' : '○');
      children.add(Padding(
        padding: const EdgeInsets.only(top: 6),
        child: Text('$mark $opt',
            style: TextStyle(
                color: isChosen ? t.text : t.textSecondary,
                fontWeight: isChosen ? FontWeight.w700 : FontWeight.w400)),
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

Widget agyAskPermissionDetail(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  final action = toolInputStr(m['Action']);
  final target = toolInputStr(m['Target']);
  final reason = toolInputStr(m['Reason']);
  final body = agyResultBody(it.result ?? '');
  return _withResult(
    [
      if (action.isNotEmpty)
        toolHeader('$action${target.isNotEmpty ? '($target)' : ''}', t),
      if (reason.isNotEmpty)
        Padding(
          padding: const EdgeInsets.only(top: 2),
          child: Text(reason, style: codeMono.copyWith(color: t.textDim)),
        ),
    ],
    it,
    body.isEmpty ? null : appMarkdown(body), t,
  );
}

Widget agyListPermissionsDetail(Entry it, ArgusTokens t) {
  final body = agyResultBody(it.result ?? '');
  if (body.isEmpty) return _generic(it, t);
  return codeBlock(body);
}

Widget agySendMessageDetail(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  final message = toolInputStr(m['Message']);
  final recipient = toolInputStr(m['Recipient']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (recipient.isNotEmpty)
      toolHeader('→ $recipient', t),
    if (message.isNotEmpty)
      appMarkdown(message)
    else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
  ]);
}

Widget agyScheduleDetail(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  final duration = toolInputStr(m['DurationSeconds']);
  final prompt = toolInputStr(m['Prompt']);
  final condition = toolInputStr(m['TimerCondition']);
  final body = agyResultBody(it.result ?? '');
  return _withResult(
    [
      if (duration.isNotEmpty)
        toolHeader('Timer ${duration}s${condition.isNotEmpty ? '  (condition: $condition)' : ''}', t),
      if (prompt.isNotEmpty) toolComment(prompt, t),
    ],
    it,
    body.isEmpty ? null : appMarkdown(body), t,
  );
}

Widget _generic(Entry it, ArgusTokens t) =>
    toolGeneric(it, t, resultText: agyResultBody);
