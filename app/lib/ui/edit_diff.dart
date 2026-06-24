import 'dart:convert';

import 'package:flutter/material.dart';

import '../models/chunk.dart';
import 'theme.dart';

const _red = Color(0xFFfb4934);
const _green = Color(0xFFb8bb26);
const _mono = TextStyle(fontFamily: 'monospace', fontSize: 12, height: 1.35);

/// Reads a tool-input field as a string, treating non-strings (incl. null) as ''.
String toolInputStr(Object? v) => v is String ? v : '';

Widget editDiffView(Item item) {
  Map<String, dynamic>? input;
  try {
    input = jsonDecode(item.toolInput ?? '') as Map<String, dynamic>;
  } catch (_) {
    input = null;
  }
  if (input == null) {
    return Text(item.toolInput ?? '',
        style: _mono.copyWith(color: AppColors.text));
  }

  final path = (input['file_path'] ?? input['notebook_path']) as String?;
  final blocks = <Widget>[];
  if (path != null && path.isNotEmpty) {
    blocks.add(Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Text('● $path', style: _mono.copyWith(color: AppColors.dim)),
    ));
  }

  switch (item.toolName) {
    case 'Edit':
      if ((input['replace_all'] as bool?) ?? false) {
        blocks.add(Padding(
          padding: const EdgeInsets.only(bottom: 4),
          child: Text('(replace all)',
              style: _mono.copyWith(color: AppColors.dim)),
        ));
      }
      blocks.addAll(_pair(
          toolInputStr(input['old_string']), toolInputStr(input['new_string'])));
      break;
    case 'MultiEdit':
      final edits = (input['edits'] as List?) ?? const [];
      for (var i = 0; i < edits.length; i++) {
        final e = edits[i] as Map<String, dynamic>;
        if (i > 0) {
          blocks.add(Padding(
            padding: const EdgeInsets.symmetric(vertical: 6),
            child: Text('─── edit ${i + 1} ───',
                style: _mono.copyWith(color: AppColors.dim)),
          ));
        }
        blocks.addAll(
            _pair(toolInputStr(e['old_string']), toolInputStr(e['new_string'])));
      }
      break;
    case 'Write':
      blocks.add(_block(toolInputStr(input['content']), added: true));
      break;
    case 'NotebookEdit':
      blocks.add(_block(toolInputStr(input['new_source']), added: true));
      break;
    default:
      blocks.add(_block(item.toolInput ?? '', added: true));
  }

  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: blocks);
}

List<Widget> _pair(String oldS, String newS) => [
      if (oldS.isNotEmpty) _block(oldS, added: false),
      if (newS.isNotEmpty) _block(newS, added: true),
    ];

Widget _block(String text, {required bool added}) {
  if (text.isEmpty) return const SizedBox.shrink();
  final color = added ? _green : _red;
  final prefix = added ? '+ ' : '- ';
  final lines = text.split('\n').map((l) => '$prefix$l').join('\n');
  return Container(
    width: double.infinity,
    margin: const EdgeInsets.only(bottom: 6),
    padding: const EdgeInsets.all(8),
    decoration: BoxDecoration(
      color: color.withValues(alpha: 0.10),
      border: Border(left: BorderSide(color: color, width: 3)),
    ),
    child: Text(lines, style: _mono.copyWith(color: AppColors.text)),
  );
}
