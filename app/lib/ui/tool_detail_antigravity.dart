import 'dart:convert';

import 'package:flutter/material.dart';

import '../models/chunk.dart';
import 'code_block.dart';
import 'edit_diff.dart';
import 'theme.dart';

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

Widget _comment(String text) =>
    Text('# $text', style: _mono.copyWith(color: AppColors.dim));

Widget _withResult(List<Widget> head, Item it, Widget? resultBody) => Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        ...head,
        if (resultBody != null) ...[
          _label(it.resultIsError ? 'Error' : 'Result', error: it.resultIsError),
          resultBody,
        ],
      ],
    );

Widget _kvDump(String s) {
  final rows = <Widget>[];
  for (final raw in s.split('\n')) {
    final line = raw.trimRight();
    if (line.trim().isEmpty) continue;
    final idx = line.indexOf(':');
    if (idx > 0) {
      rows.add(RichText(
        text: TextSpan(style: _mono, children: [
          TextSpan(
              text: line.substring(0, idx + 1),
              style: _mono.copyWith(color: AppColors.dim)),
          TextSpan(
              text: line.substring(idx + 1),
              style: _mono.copyWith(color: AppColors.text)),
        ]),
      ));
    } else {
      rows.add(Text(line, style: _mono.copyWith(color: AppColors.text)));
    }
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: rows);
}

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

Widget agyRunCommandDetail(Item it) {
  final m = _input(it);
  final cmd = toolInputStr(m['CommandLine']);
  final cwd = toolInputStr(m['Cwd']);
  final head = <Widget>[
    if (cwd.isNotEmpty) _comment(cwd),
    if (cmd.isNotEmpty)
      codeBlock(cmd, lang: 'bash')
    else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
  ];
  Widget? body;
  if ((it.result ?? '').isNotEmpty) {
    final r = splitRunCommandResult(it.result!);
    body = Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      if (r.head.trim().isNotEmpty) _kvDump(r.head),
      if (r.output.isNotEmpty) codeBlock(r.output),
      if (!r.hasMarker && r.head.trim().isEmpty) codeBlock(it.result!),
    ]);
  }
  return _withResult(head, it, body);
}

Widget agyGrepSearchDetail(Item it) {
  final m = _input(it);
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
              style: _mono.copyWith(
                  color: AppColors.secondary, fontWeight: FontWeight.w700)),
          if (path.isNotEmpty)
            TextSpan(
                text: ' in $path', style: _mono.copyWith(color: AppColors.dim)),
          if (flags.isNotEmpty)
            TextSpan(
                text: '  (${flags.join(' · ')})',
                style: _mono.copyWith(color: AppColors.dim)),
        ]),
      ),
  ];
  final rows = grepRows(agyResultBody(it.result ?? ''));
  return _withResult(
      head, it, rows.isEmpty ? null : codeBlock(rows.join('\n')));
}

Widget agyListDirDetail(Item it) {
  final path = toolInputStr(_input(it)['DirectoryPath']);
  final rows = listDirRows(agyResultBody(it.result ?? ''));
  return _withResult(
    [if (path.isNotEmpty) _header(path)],
    it,
    rows.isEmpty ? null : codeBlock(rows.join('\n')),
  );
}

Widget agyViewFileDetail(Item it) {
  final path = toolInputStr(_input(it)['AbsolutePath']);
  final (:meta, :content) = splitViewFileResult(it.result ?? '');
  Widget? body;
  if (content.isNotEmpty) {
    body = codeBlock(content, lineNumberToggle: false);
  } else if ((it.result ?? '').isNotEmpty) {
    body = codeBlock(agyResultBody(it.result!));
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (path.isNotEmpty) _header(path),
    if (meta.isNotEmpty)
      Padding(
        padding: const EdgeInsets.only(top: 2),
        child: Text(meta, style: _mono.copyWith(color: AppColors.dim)),
      ),
    if (body != null) ...[
      _label(it.resultIsError ? 'Error' : 'Result', error: it.resultIsError),
      body,
    ],
  ]);
}

Widget agyWriteToFileDetail(Item it) {
  final m = _input(it);
  final path = toolInputStr(m['TargetFile']);
  final desc = toolInputStr(m['Description']);
  final code = toolInputStr(m['CodeContent']);
  final overwrite = m['Overwrite'] as bool? ?? false;
  final head = <Widget>[
    if (desc.isNotEmpty) _comment(desc),
    if (path.isNotEmpty)
      Text('● $path${overwrite ? '  (overwrite)' : ''}',
          style: _mono.copyWith(color: AppColors.dim)),
    if (code.isNotEmpty)
      diffView('', code)
    else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
  ];
  final body = (it.result ?? '').isNotEmpty
      ? appMarkdown(stripAgyBoilerplate(agyResultBody(it.result!)))
      : null;
  return _withResult(head, it, body);
}

Widget agyReplaceFileContentDetail(Item it) => _replaceDetail(it, multi: false);
Widget agyMultiReplaceFileContentDetail(Item it) =>
    _replaceDetail(it, multi: true);

Widget _replaceDetail(Item it, {required bool multi}) {
  final m = _input(it);
  final path = toolInputStr(m['TargetFile']);
  final desc = toolInputStr(m['Description']);
  final children = <Widget>[
    if (desc.isNotEmpty) _comment(desc),
  ];
  if (multi) {
    final chunks = (m['ReplacementChunks'] as List?) ?? const [];
    if (path.isNotEmpty) {
      children.add(Text('● $path${chunks.isEmpty ? '' : '  (${chunks.length} edits)'}',
          style: _mono.copyWith(color: AppColors.dim)));
    }
    for (var i = 0; i < chunks.length; i++) {
      final c = chunks[i] as Map<String, dynamic>;
      children.add(Padding(
        padding: const EdgeInsets.only(top: 6, bottom: 2),
        child: Text(
            '─── edit ${i + 1} (lines ${c['StartLine'] ?? 0}–${c['EndLine'] ?? 0}) ───',
            style: _mono.copyWith(color: AppColors.dim)),
      ));
      children.add(diffView(toolInputStr(c['TargetContent']),
          toolInputStr(c['ReplacementContent'])));
    }
    if (chunks.isEmpty && (it.toolInput ?? '').isNotEmpty) {
      children.add(codeBlock(it.toolInput!));
    }
  } else {
    final start = (m['StartLine'] as num?)?.toInt() ?? 0;
    final end = (m['EndLine'] as num?)?.toInt() ?? 0;
    if (path.isNotEmpty) {
      children.add(Text('● $path${start > 0 || end > 0 ? '  (lines $start–$end)' : ''}',
          style: _mono.copyWith(color: AppColors.dim)));
    }
    final target = toolInputStr(m['TargetContent']);
    final replacement = toolInputStr(m['ReplacementContent']);
    if (target.isNotEmpty || replacement.isNotEmpty) {
      children.add(diffView(target, replacement));
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
        : null,
  );
}

Widget agySearchWebDetail(Item it) {
  final m = _input(it);
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
              style: _mono.copyWith(
                  color: AppColors.secondary, fontWeight: FontWeight.w700)),
          if (domain.isNotEmpty)
            TextSpan(
                text: '  $domain', style: _mono.copyWith(color: AppColors.dim)),
        ]),
      ),
    if (body.isNotEmpty) ...[
      _label('Result'),
      appMarkdown(body),
    ],
  ]);
}

Widget agyGenerateImageDetail(Item it) {
  final m = _input(it);
  final name = toolInputStr(m['ImageName']);
  final ratio = toolInputStr(m['AspectRatio']);
  final prompt = toolInputStr(m['Prompt']);
  final head = <Widget>[
    if (name.isNotEmpty)
      Text('$name${ratio.isNotEmpty ? '  ($ratio)' : ''}',
          style: _mono.copyWith(
              color: AppColors.secondary, fontWeight: FontWeight.w700)),
    if (prompt.isNotEmpty) _comment(prompt),
  ];
  final img = agyImageResult(it.result ?? '');
  return _withResult(head, it, img.isEmpty ? null : codeBlock(img));
}

Widget agyDefineSubagentDetail(Item it) {
  final m = _input(it);
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
      Text(name,
          style: _mono.copyWith(
              color: AppColors.secondary, fontWeight: FontWeight.w700)),
    if (desc.isNotEmpty)
      Padding(
        padding: const EdgeInsets.only(top: 2),
        child: Text(desc, style: _mono.copyWith(color: AppColors.dim)),
      ),
    Padding(
      padding: const EdgeInsets.only(top: 2),
      child: Text('tools: ${tools.isEmpty ? 'none' : tools.join(' · ')}',
          style: _mono.copyWith(color: AppColors.dim)),
    ),
    if (prompt.isNotEmpty) ...[
      _label('System Prompt'),
      appMarkdown(prompt),
    ],
  ];
  return _withResult(
    children,
    it,
    it.resultIsError && (it.result ?? '').isNotEmpty
        ? codeBlock(agyResultBody(it.result!))
        : null,
  );
}

Widget agyManageSubagentsDetail(Item it) {
  final action = toolInputStr(_input(it)['Action']);
  final body = agyResultBody(it.result ?? '');
  return _withResult(
    [
      if (action.isNotEmpty)
        Text(action,
            style: _mono.copyWith(
                color: AppColors.secondary, fontWeight: FontWeight.w700)),
    ],
    it,
    body.isEmpty ? null : appMarkdown(body),
  );
}

Widget agyManageTaskDetail(Item it) {
  final m = _input(it);
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
                style: _mono.copyWith(
                    color: AppColors.secondary, fontWeight: FontWeight.w700)),
            if (taskId.isNotEmpty)
              TextSpan(
                  text: '  $taskId',
                  style: _mono.copyWith(color: AppColors.dim)),
          ]),
        ),
    ],
    it,
    body.isEmpty ? null : _kvDump(body),
  );
}

Widget agyAskQuestionDetail(Item it) {
  final qs = (_input(it)['questions'] as List?) ?? const [];
  if (qs.isEmpty) return _generic(it);
  final answers = parseAgyAnswers(agyResultBody(it.result ?? ''));
  final blocks = <Widget>[];
  for (var qi = 0; qi < qs.length; qi++) {
    final q = qs[qi] as Map<String, dynamic>;
    final question = toolInputStr(q['question']);
    final multi = q['is_multi_select'] as bool? ?? false;
    final ans = answers[qi] ?? '';
    final children = <Widget>[
      Row(children: [
        const Icon(Icons.chat_bubble_outline, size: 14, color: AppColors.accent),
        const SizedBox(width: 6),
        Text('Agent is asking',
            style: _mono.copyWith(
                color: AppColors.accent, fontWeight: FontWeight.w700)),
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
                color: isChosen ? AppColors.text : AppColors.secondary,
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

Widget agyAskPermissionDetail(Item it) {
  final m = _input(it);
  final action = toolInputStr(m['Action']);
  final target = toolInputStr(m['Target']);
  final reason = toolInputStr(m['Reason']);
  final body = agyResultBody(it.result ?? '');
  return _withResult(
    [
      if (action.isNotEmpty)
        Text('$action${target.isNotEmpty ? '($target)' : ''}',
            style: _mono.copyWith(
                color: AppColors.secondary, fontWeight: FontWeight.w700)),
      if (reason.isNotEmpty)
        Padding(
          padding: const EdgeInsets.only(top: 2),
          child: Text(reason, style: _mono.copyWith(color: AppColors.dim)),
        ),
    ],
    it,
    body.isEmpty ? null : appMarkdown(body),
  );
}

Widget agyListPermissionsDetail(Item it) {
  final body = agyResultBody(it.result ?? '');
  if (body.isEmpty) return _generic(it);
  return codeBlock(body);
}

Widget agySendMessageDetail(Item it) {
  final m = _input(it);
  final message = toolInputStr(m['Message']);
  final recipient = toolInputStr(m['Recipient']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (recipient.isNotEmpty)
      Text('→ $recipient',
          style: _mono.copyWith(
              color: AppColors.secondary, fontWeight: FontWeight.w700)),
    if (message.isNotEmpty)
      appMarkdown(message)
    else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
  ]);
}

Widget agyScheduleDetail(Item it) {
  final m = _input(it);
  final duration = toolInputStr(m['DurationSeconds']);
  final prompt = toolInputStr(m['Prompt']);
  final condition = toolInputStr(m['TimerCondition']);
  final body = agyResultBody(it.result ?? '');
  return _withResult(
    [
      if (duration.isNotEmpty)
        Text('Timer ${duration}s${condition.isNotEmpty ? '  (condition: $condition)' : ''}',
            style: _mono.copyWith(
                color: AppColors.secondary, fontWeight: FontWeight.w700)),
      if (prompt.isNotEmpty) _comment(prompt),
    ],
    it,
    body.isEmpty ? null : appMarkdown(body),
  );
}

Widget _generic(Item it) =>
    Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      if ((it.toolInput ?? '').isNotEmpty) ...[
        _label('Input'),
        codeBlock(it.toolInput!),
      ],
      if ((it.result ?? '').isNotEmpty) ...[
        _label(it.resultIsError ? 'Error' : 'Result', error: it.resultIsError),
        codeBlock(agyResultBody(it.result!)),
      ],
    ]);
