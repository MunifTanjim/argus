import 'dart:convert';

import 'package:flutter/material.dart';

import '../models/entry.dart';
import 'theme.dart';
import 'tool_detail_common.dart';

String _str(Object? v) => v is String ? v : '';

Widget claudeTaskCreateDetail(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  final subject = _str(m['subject']);
  if (subject.isEmpty && (it.toolInput ?? '').isNotEmpty) {
    return toolGeneric(it, t);
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    toolHeader(subject, t),
    if (_str(m['activeForm']).isNotEmpty)
      Padding(
        padding: const EdgeInsets.only(top: 2),
        child: Text(_str(m['activeForm']),
            style: codeMono.copyWith(color: t.textDim)),
      ),
    if (_str(m['description']).isNotEmpty)
      Padding(
        padding: const EdgeInsets.only(top: 4),
        child: Text(_str(m['description']),
            style: codeMono.copyWith(color: t.text)),
      ),
  ]);
}

Widget claudeTaskUpdateDetail(Entry it, ArgusTokens t) {
  final m = toolInput(it);
  final taskId = _str(m['taskId']);
  final rows = <Widget>[];
  if (taskId.isNotEmpty) {
    rows.add(toolHeader('Task $taskId', t));
  }
  for (final e in m.entries) {
    if (e.key == 'taskId') continue;
    rows.add(RichText(
      text: TextSpan(style: codeMono, children: [
        TextSpan(
            text: '${e.key}: ',
            style: codeMono.copyWith(color: t.textDim)),
        TextSpan(
            text: e.value is String ? e.value as String : jsonEncode(e.value),
            style: codeMono.copyWith(color: t.text)),
      ]),
    ));
  }
  if (rows.isEmpty) return toolGeneric(it, t);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: rows);
}
