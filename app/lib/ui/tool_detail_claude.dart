import 'dart:convert';

import 'package:flutter/material.dart';

import '../models/chunk.dart';
import 'code_block.dart';
import 'theme.dart';

const _mono = TextStyle(fontFamily: 'monospace', fontSize: 12, height: 1.35);

Map<String, dynamic> _input(Item it) {
  try {
    return jsonDecode(it.toolInput ?? '') as Map<String, dynamic>;
  } catch (_) {
    return const {};
  }
}

String _str(Object? v) => v is String ? v : '';

Widget _label(String text) => Padding(
      padding: const EdgeInsets.only(top: 8, bottom: 4),
      child: Text(text,
          style: const TextStyle(
              color: AppColors.secondary,
              fontWeight: FontWeight.w700,
              fontSize: 13)),
    );

Widget claudeTaskCreateDetail(Item it) {
  final m = _input(it);
  final subject = _str(m['subject']);
  if (subject.isEmpty && (it.toolInput ?? '').isNotEmpty) {
    return _generic(it);
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    Text(subject,
        style: _mono.copyWith(
            color: AppColors.secondary, fontWeight: FontWeight.w700)),
    if (_str(m['activeForm']).isNotEmpty)
      Padding(
        padding: const EdgeInsets.only(top: 2),
        child: Text(_str(m['activeForm']),
            style: _mono.copyWith(color: AppColors.dim)),
      ),
    if (_str(m['description']).isNotEmpty)
      Padding(
        padding: const EdgeInsets.only(top: 4),
        child: Text(_str(m['description']),
            style: _mono.copyWith(color: AppColors.text)),
      ),
  ]);
}

Widget claudeTaskUpdateDetail(Item it) {
  final m = _input(it);
  final taskId = _str(m['taskId']);
  final rows = <Widget>[];
  if (taskId.isNotEmpty) {
    rows.add(Text('Task $taskId',
        style: _mono.copyWith(
            color: AppColors.secondary, fontWeight: FontWeight.w700)));
  }
  for (final e in m.entries) {
    if (e.key == 'taskId') continue;
    rows.add(RichText(
      text: TextSpan(style: _mono, children: [
        TextSpan(
            text: '${e.key}: ',
            style: _mono.copyWith(color: AppColors.dim)),
        TextSpan(
            text: e.value is String ? e.value as String : jsonEncode(e.value),
            style: _mono.copyWith(color: AppColors.text)),
      ]),
    ));
  }
  if (rows.isEmpty) return _generic(it);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: rows);
}

Widget _generic(Item it) =>
    Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      if ((it.toolInput ?? '').isNotEmpty) ...[
        _label('Input'),
        codeBlock(it.toolInput!),
      ],
      if ((it.result ?? '').isNotEmpty) ...[
        _label(it.resultIsError ? 'Error' : 'Result'),
        codeBlock(it.result!),
      ],
    ]);
