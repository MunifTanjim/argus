import 'dart:convert';

import 'package:flutter/material.dart';

import '../models/entry.dart';
import 'code_block.dart';
import 'edit_diff.dart';
import 'theme.dart';
import 'tool_detail.dart';

const _red = Color(0xFFfb4934);
const _mono = TextStyle(fontFamily: 'monospace', fontSize: 12, height: 1.35);

Map<String, dynamic> _input(Entry it) {
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

Widget _bold(String text) => Text(text,
    style:
        _mono.copyWith(color: AppColors.secondary, fontWeight: FontWeight.w700));

Widget _comment(String text) =>
    Text('# $text', style: _mono.copyWith(color: AppColors.dim));

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

Widget _resultSection(Entry it, Widget? body) => body == null
    ? const SizedBox.shrink()
    : Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        _label(it.resultIsError ? 'Error' : 'Result', error: it.resultIsError),
        body,
      ]);

/// An exec-style result: the key/value head, then the output as code.
Widget _execResultBody(String result) {
  final r = splitExecResult(result);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (r.head.trim().isNotEmpty) _kvDump(r.head),
    if (r.output.isNotEmpty) codeBlock(r.output),
  ]);
}

const _execOutputMarker = 'Output:\n';

({String head, String output, bool hasMarker}) splitExecResult(String result) {
  final idx = result.indexOf(_execOutputMarker);
  if (idx < 0) return (head: result, output: '', hasMarker: false);
  return (
    head: '${result.substring(0, idx)}Output:',
    output: result.substring(idx + _execOutputMarker.length),
    hasMarker: true,
  );
}

/// Extracts a (state, message) pair from `{"state":"message"}`; null otherwise.
({String state, String message})? agentStatus(Object? raw) {
  // Codex states that carry no message (e.g. "running") serialize bare.
  if (raw is String && raw.isNotEmpty) return (state: raw, message: '');
  if (raw is! Map || raw.isEmpty) return null;
  final entry = raw.entries.first;
  return (state: '${entry.key}', message: '${entry.value}');
}

String agentName(Entry it, String id) {
  for (final s in it.subagents) {
    if (s.id == id && s.name.isNotEmpty) return s.name;
  }
  return id;
}

Widget codexExecCommandDetail(Entry it) {
  final m = _input(it);
  final cmd = toolInputStr(m['cmd']);
  final workdir = toolInputStr(m['workdir']);
  final yieldMs = (m['yield_time_ms'] as num?)?.toInt() ?? 0;
  final maxTokens = (m['max_output_tokens'] as num?)?.toInt() ?? 0;
  final meta = [
    if (yieldMs > 0) 'yield ${yieldMs}ms',
    if (maxTokens > 0) 'max $maxTokens tokens',
  ];
  final head = <Widget>[
    if (cmd.isNotEmpty) ...[
      if (workdir.isNotEmpty) _comment(workdir),
      _bold('\$ $cmd'),
      if (meta.isNotEmpty)
        Text(meta.join(' · '), style: _mono.copyWith(color: AppColors.dim)),
    ] else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
  ];
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    ...head,
    if ((it.result ?? '').isNotEmpty)
      _resultSection(it, _execResultBody(it.result!)),
  ]);
}

Widget codexUpdatePlanDetail(Entry it) {
  final plan = (_input(it)['plan'] as List?) ?? const [];
  if (plan.isEmpty) return _generic(it);
  final rows = <Widget>[];
  for (final p in plan.cast<Map<String, dynamic>>()) {
    final status = toolInputStr(p['status']);
    final glyph = status == 'completed'
        ? '☑'
        : status == 'in_progress'
            ? '◐'
            : '☐';
    rows.add(Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Text('$glyph ${toolInputStr(p['step'])}',
          style: _mono.copyWith(color: AppColors.text)),
    ));
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: rows);
}

Widget codexWebSearchDetail(Entry it) {
  final m = _input(it);
  final query = toolInputStr(m['query']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (query.isNotEmpty) _bold(query),
    if ((it.result ?? '').isNotEmpty) ...[
      _label('Result'),
      appMarkdown(it.result!),
    ],
  ]);
}

Widget codexWaitAgentDetail(Entry it) {
  final m = _input(it);
  final targets = ((m['targets'] as List?) ?? const []).map((e) => '$e').toList();
  final timeoutMs = (m['timeout_ms'] as num?)?.toInt() ?? 0;
  final head = <Widget>[];
  if (targets.isNotEmpty) {
    final names = targets.map((id) => agentName(it, id)).join(', ');
    head.add(RichText(
      text: TextSpan(children: [
        TextSpan(text: 'Waiting on ', style: _mono.copyWith(color: AppColors.secondary, fontWeight: FontWeight.w700)),
        TextSpan(text: names, style: _mono.copyWith(color: AppColors.text)),
        if (timeoutMs > 0)
          TextSpan(text: '  (timeout ${timeoutMs}ms)', style: _mono.copyWith(color: AppColors.dim)),
      ]),
    ));
  } else if ((it.toolInput ?? '').isNotEmpty) {
    head.add(codeBlock(it.toolInput!));
  }

  Widget? body;
  final status = _resultStatus(it.result, 'status');
  if (status != null) {
    final ids = targets.isNotEmpty ? targets : status.keys.toList();
    body = Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        for (final id in ids)
          if (status[id] != null) _statusBlock(agentName(it, id), status[id]),
      ],
    );
  } else if ((it.result ?? '').isNotEmpty) {
    body = appMarkdown(it.result!);
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    ...head,
    _resultSection(it, body),
  ]);
}

Widget codexCloseAgentDetail(Entry it) {
  final target = toolInputStr(_input(it)['target']);
  final head = <Widget>[
    if (target.isNotEmpty)
      RichText(
        text: TextSpan(children: [
          TextSpan(text: 'Closed ', style: _mono.copyWith(color: AppColors.secondary, fontWeight: FontWeight.w700)),
          TextSpan(text: agentName(it, target), style: _mono.copyWith(color: AppColors.text)),
        ]),
      )
    else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
  ];
  Widget? body;
  final prev = _resultField(it.result, 'previous_status');
  if (prev != null) {
    body = _statusBlock(agentName(it, target), prev);
  } else if ((it.result ?? '').isNotEmpty) {
    body = appMarkdown(it.result!);
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    ...head,
    _resultSection(it, body),
  ]);
}

Widget _statusBlock(String name, Object? raw) {
  final s = agentStatus(raw);
  if (s == null) return _bold(name);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    RichText(
      text: TextSpan(children: [
        TextSpan(text: name, style: _mono.copyWith(color: AppColors.secondary, fontWeight: FontWeight.w700)),
        TextSpan(text: ': ${s.state}', style: _mono.copyWith(color: AppColors.secondary)),
      ]),
    ),
    if (s.message.isNotEmpty) appMarkdown(s.message),
  ]);
}

Map<String, dynamic>? _resultStatus(String? result, String key) {
  final v = _resultField(result, key);
  return v is Map<String, dynamic> ? v : null;
}

Object? _resultField(String? result, String key) {
  if (result == null || result.isEmpty) return null;
  try {
    final m = jsonDecode(result);
    if (m is Map<String, dynamic>) return m[key];
  } catch (_) {}
  return null;
}

Widget _generic(Entry it) =>
    Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      if ((it.toolInput ?? '').isNotEmpty) ...[
        _label('Input'),
        codeBlock(it.toolInput!),
      ],
      if ((it.result ?? '').isNotEmpty)
        _resultSection(it, codeBlock(it.result!)),
    ]);

const _green = Color(0xFFb8bb26);
final _patchAdd = _mono.copyWith(color: _green);
final _patchDel = _mono.copyWith(color: _red);
final _patchCtx = _mono.copyWith(color: AppColors.dim);

class CodexPatchLine {
  final String kind; // '+', '-', ' ', or '@' (hunk header)
  final String text;
  const CodexPatchLine(this.kind, this.text);
}

class CodexPatchFile {
  final String op; // add | update | delete
  final String path;
  String moveTo = '';
  final List<CodexPatchLine> lines = [];
  CodexPatchFile(this.op, this.path);
}

/// Parses a Codex apply_patch input; null when it is not a patch (mirrors
/// internal/codextool.ParsePatch).
List<CodexPatchFile>? parseCodexPatch(String input) {
  final lines = input.replaceAll('\r\n', '\n').trimRight().split('\n');
  var i = 0;
  while (i < lines.length && lines[i].trim().isEmpty) {
    i++;
  }
  if (i >= lines.length || lines[i].trim() != '*** Begin Patch') return null;
  final files = <CodexPatchFile>[];
  for (final ln in lines.skip(i + 1)) {
    final t = ln.trimRight();
    if (t == '*** End Patch') return files.isEmpty ? null : files;
    if (t.startsWith('*** Add File: ')) {
      files.add(CodexPatchFile('add', t.substring(14)));
    } else if (t.startsWith('*** Update File: ')) {
      files.add(CodexPatchFile('update', t.substring(17)));
    } else if (t.startsWith('*** Delete File: ')) {
      files.add(CodexPatchFile('delete', t.substring(17)));
    } else if (t.startsWith('*** Move to: ')) {
      if (files.isNotEmpty) files.last.moveTo = t.substring(13);
    } else if (t == '*** End of File') {
      continue;
    } else if (files.isEmpty) {
      return null;
    } else if (ln.startsWith('@@')) {
      files.last.lines.add(CodexPatchLine('@', ln.substring(2).trim()));
    } else if (ln.isEmpty) {
      files.last.lines.add(const CodexPatchLine(' ', ''));
    } else if ('+- '.contains(ln[0])) {
      files.last.lines.add(CodexPatchLine(ln[0], ln.substring(1)));
    } else {
      files.last.lines.add(CodexPatchLine(' ', ln));
    }
  }
  return files.isEmpty ? null : files;
}

/// Separates a Codex answer's selected label from its "user_note: " entry.
({String label, String note}) splitCodexAnswer(List<String> vals) {
  var label = '';
  var note = '';
  for (final v in vals) {
    if (v.startsWith('user_note: ')) {
      note = v.substring(11);
    } else if (label.isEmpty) {
      label = v;
    }
  }
  return (label: label, note: note);
}

String _patchHeader(CodexPatchFile f) => switch (f.op) {
      'add' => 'A ${f.path}',
      'delete' => 'D ${f.path}',
      _ => f.moveTo.isNotEmpty ? 'M ${f.path} → ${f.moveTo}' : 'M ${f.path}',
    };

Widget codexApplyPatchDetail(Entry it) {
  final input = it.toolInput ?? '';
  final files = parseCodexPatch(input);
  final rows = <Widget>[];
  if (files == null) {
    if (input.isNotEmpty) rows.add(codeBlock(input));
  } else {
    for (final f in files) {
      rows.add(Padding(
          padding: const EdgeInsets.only(top: 6, bottom: 2),
          child: _bold(_patchHeader(f))));
      if (f.lines.isEmpty) continue;
      // One Text per file: a large patch stays a handful of widgets.
      rows.add(Text.rich(TextSpan(style: _mono, children: [
        for (final (i, l) in f.lines.indexed)
          TextSpan(
            text: (i > 0 ? '\n' : '') +
                switch (l.kind) {
                  '@' => l.text.isEmpty ? '@@' : '@@ ${l.text}',
                  ' ' => ' ${l.text}',
                  _ => '${l.kind}${l.text}',
                },
            style: switch (l.kind) {
              '+' => _patchAdd,
              '-' => _patchDel,
              _ => _patchCtx,
            },
          ),
      ])));
    }
  }
  if (it.resultIsError && (it.result ?? '').isNotEmpty) {
    final r = splitExecResult(it.result!);
    rows.add(_resultSection(it, codeBlock(r.hasMarker ? r.output : it.result!)));
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: rows);
}

Widget codexExecDetail(Entry it) {
  final input = it.toolInput ?? '';
  Widget? body;
  final result = it.result ?? '';
  if (result.isNotEmpty) {
    if (result.startsWith('aborted')) {
      body = codeBlock(result);
    } else {
      body = _execResultBody(result);
    }
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (input.isNotEmpty) codeBlock(input, lang: 'javascript'),
    _resultSection(it, body),
  ]);
}

Widget codexQuestionDetail(Entry it) {
  final answers = _resultField(it.result, 'answers');
  return questionsDetail(it, (q) {
    final raw = answers is Map ? answers[toolInputStr(q['id'])] : null;
    final vals = raw is Map
        ? ((raw['answers'] as List?) ?? const []).map((e) => '$e').toList()
        : const <String>[];
    final a = splitCodexAnswer(vals);
    return (picks: [a.label], note: a.note);
  });
}

Widget codexAsyncQuestionDetail(Entry it) {
  final qs = ((_input(it)['questions'] as List?) ?? const [])
      .whereType<Map<String, dynamic>>()
      .toList();
  if (qs.isEmpty) return _generic(it);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    for (final q in qs) ...[
      Padding(
          padding: const EdgeInsets.only(top: 6),
          child: Text(toolInputStr(q['title']),
              style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 13))),
      for (final o in (q['options'] as List?) ?? const [])
        Text('• $o', style: _mono.copyWith(color: AppColors.secondary)),
    ],
    if (it.resultIsError && (it.result ?? '').isNotEmpty)
      _resultSection(it, codeBlock(it.result!, wrap: true))
    else
      Padding(
          padding: const EdgeInsets.only(top: 8),
          child: Text('Asked without waiting; answered in a later message.',
              style: _mono.copyWith(color: AppColors.dim))),
  ]);
}

Widget codexViewImageDetail(Entry it) {
  final m = _input(it);
  final path = toolInputStr(m['path']);
  final detail = toolInputStr(m['detail']);
  final result = it.result ?? '';
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (path.isNotEmpty)
      _kvDump('path: $path${detail.isNotEmpty ? '\ndetail: $detail' : ''}')
    else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
    if (result.startsWith('[image'))
      Padding(
          padding: const EdgeInsets.only(top: 6),
          child: Text('image attached',
              style: _mono.copyWith(color: AppColors.dim)))
    else if (result.isNotEmpty)
      _resultSection(it, codeBlock(result)),
  ]);
}

/// Renders a millisecond count as a short duration ("30s", "1m30s").
String msDuration(int ms) {
  String secs(num s) => s == s.truncate() ? '${s.truncate()}s' : '${s}s';
  if (ms < 60000) return secs(ms / 1000);
  final h = ms ~/ 3600000, m = (ms % 3600000) ~/ 60000;
  final s = secs((ms % 60000) / 1000);
  return h > 0 ? '${h}h${m}m$s' : '${m}m$s';
}

Widget _headline(String lead, String rest, [String? dim]) => RichText(
      text: TextSpan(style: _mono, children: [
        TextSpan(
            text: lead,
            style: _mono.copyWith(
                color: AppColors.secondary, fontWeight: FontWeight.w700)),
        TextSpan(text: rest, style: _mono.copyWith(color: AppColors.text)),
        if (dim != null)
          TextSpan(text: '  ($dim)', style: _mono.copyWith(color: AppColors.dim)),
      ]),
    );

/// Code mode's wait on a running exec cell; the result reports the cell like
/// exec does.
Widget codexWaitCellDetail(Entry it) {
  final m = _input(it);
  final cell = toolInputStr(m['cell_id']);
  final yieldMs = (m['yield_time_ms'] as num?)?.toInt() ?? 0;
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (cell.isNotEmpty)
      _headline('Waiting on cell ', cell,
          yieldMs > 0 ? 'yield ${msDuration(yieldMs)}' : null)
    else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
    if ((it.result ?? '').isNotEmpty)
      _resultSection(it, _execResultBody(it.result!)),
  ]);
}

Widget codexSleepDetail(Entry it) {
  final ms = (_input(it)['duration_ms'] as num?)?.toInt() ?? 0;
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (ms > 0)
      _headline('Sleep ', msDuration(ms))
    else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
    if ((it.result ?? '').isNotEmpty)
      _resultSection(it, codeBlock(it.result!)),
  ]);
}

/// Multi-agent v2's wait_agent waits on any agent: the input is only a timeout.
Widget codexV2WaitAgentDetail(Entry it) {
  final timeoutMs = (_input(it)['timeout_ms'] as num?)?.toInt() ?? 0;
  final result = it.result ?? '';
  Widget? body;
  if (result.isNotEmpty) {
    final message = _resultField(result, 'message');
    final timedOut = _resultField(result, 'timed_out') == true;
    body = message is String || timedOut
        ? Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            if (timedOut)
              Text('timed out', style: _mono.copyWith(color: AppColors.dim)),
            if (message is String && message.isNotEmpty) appMarkdown(message),
          ])
        : codeBlock(result);
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    _headline('Waiting on agents', '',
        timeoutMs > 0 ? 'timeout ${msDuration(timeoutMs)}' : null),
    _resultSection(it, body),
  ]);
}

/// Codex sends encrypted content as Fernet tokens with this prefix.
const _encryptedPrefix = 'gAAAAA';

/// Multi-agent v2's send_message and followup_task: a message to the agent at
/// target. An encrypted message leaves only the target readable.
Widget codexAgentMessageDetail(Entry it) {
  final m = _input(it);
  final target = toolInputStr(m['target']);
  if (target.isEmpty) return _generic(it);
  final message = toolInputStr(m['message']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    _headline('To ', target),
    if (message.startsWith(_encryptedPrefix))
      Text('message encrypted', style: _mono.copyWith(color: AppColors.dim))
    else if (message.isNotEmpty)
      appMarkdown(message),
    if ((it.result ?? '').isNotEmpty)
      _resultSection(it, codeBlock(it.result!)),
  ]);
}

Widget codexInterruptAgentDetail(Entry it) {
  final target = toolInputStr(_input(it)['target']);
  if (target.isEmpty) return _generic(it);
  final result = it.result ?? '';
  final prev = _resultField(result, 'previous_status');
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    _headline('Interrupted ', target),
    if (result.isNotEmpty)
      _resultSection(it,
          prev != null ? _statusBlock('Previous status', prev) : codeBlock(result)),
  ]);
}

Widget codexListAgentsDetail(Entry it) {
  final prefix = toolInputStr(_input(it)['path_prefix']);
  final result = it.result ?? '';
  final agents = _resultField(result, 'agents');
  Widget? body;
  if (agents is List) {
    body = agents.isEmpty
        ? Text('no agents', style: _mono.copyWith(color: AppColors.dim))
        : Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            for (final a in agents.whereType<Map<String, dynamic>>())
              _statusBlock(toolInputStr(a['agent_name']), a['agent_status']),
          ]);
  } else if (result.isNotEmpty) {
    body = codeBlock(result);
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (prefix.isNotEmpty) _headline('Agents under ', prefix),
    _resultSection(it, body),
  ]);
}
