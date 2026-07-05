import 'dart:convert';

import 'package:flutter/material.dart';

import '../models/chunk.dart';
import 'code_block.dart';
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

Widget _resultSection(Item it, Widget? body) => body == null
    ? const SizedBox.shrink()
    : Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        _label(it.resultIsError ? 'Error' : 'Result', error: it.resultIsError),
        body,
      ]);

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
  if (raw is! Map || raw.isEmpty) return null;
  final entry = raw.entries.first;
  return (state: '${entry.key}', message: '${entry.value}');
}

String agentName(Item it, String id) {
  for (final s in it.subagents) {
    if (s.id == id && s.name.isNotEmpty) return s.name;
  }
  return id;
}

Widget codexExecCommandDetail(Item it) {
  final m = _input(it);
  final cmd = _str(m['cmd']);
  final workdir = _str(m['workdir']);
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
  Widget? body;
  if ((it.result ?? '').isNotEmpty) {
    final r = splitExecResult(it.result!);
    body = Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      if (r.head.trim().isNotEmpty) _kvDump(r.head),
      if (r.output.isNotEmpty) codeBlock(r.output),
    ]);
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    ...head,
    _resultSection(it, body),
  ]);
}

Widget codexUpdatePlanDetail(Item it) {
  final plan = (_input(it)['plan'] as List?) ?? const [];
  if (plan.isEmpty) return _generic(it);
  final rows = <Widget>[];
  for (final p in plan.cast<Map<String, dynamic>>()) {
    final status = _str(p['status']);
    final glyph = status == 'completed'
        ? '☑'
        : status == 'in_progress'
            ? '◐'
            : '☐';
    rows.add(Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Text('$glyph ${_str(p['step'])}',
          style: _mono.copyWith(color: AppColors.text)),
    ));
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: rows);
}

Widget codexWebSearchDetail(Item it) {
  final m = _input(it);
  final query = _str(m['query']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (query.isNotEmpty) _bold(query),
    if ((it.result ?? '').isNotEmpty) ...[
      _label('Result'),
      appMarkdown(it.result!),
    ],
  ]);
}

Widget codexWaitAgentDetail(Item it) {
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

Widget codexCloseAgentDetail(Item it) {
  final target = _str(_input(it)['target']);
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

String _str(Object? v) => v is String ? v : '';

Widget _generic(Item it) =>
    Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      if ((it.toolInput ?? '').isNotEmpty) ...[
        _label('Input'),
        codeBlock(it.toolInput!),
      ],
      if ((it.result ?? '').isNotEmpty) ...[
        _label(it.resultIsError ? 'Error' : 'Result', error: it.resultIsError),
        codeBlock(it.result!),
      ],
    ]);
