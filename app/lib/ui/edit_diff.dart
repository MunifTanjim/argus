import 'dart:convert';

import 'package:flutter/material.dart';

import '../models/chunk.dart';
import 'code_block.dart';
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
      blocks.add(_diff(
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
        blocks.add(_diff(
            toolInputStr(e['old_string']), toolInputStr(e['new_string'])));
      }
      break;
    case 'Write':
      blocks.add(_diff('', toolInputStr(input['content'])));
      break;
    case 'NotebookEdit':
      blocks.add(_diff('', toolInputStr(input['new_source'])));
      break;
    default:
      blocks.add(_diff('', item.toolInput ?? ''));
  }

  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: blocks);
}

enum _DKind { context, add, del }

class _DLine {
  const _DLine(this.text, this.kind);
  final String text;
  final _DKind kind;
}

/// Renders an interleaved line-level diff of [oldS] → [newS].
Widget _diff(String oldS, String newS) => _diffBox(_lineDiff(oldS, newS));

Widget _diffBox(List<_DLine> lines) {
  if (lines.isEmpty) return const SizedBox.shrink();
  final spans = <TextSpan>[];
  for (var i = 0; i < lines.length; i++) {
    final l = lines[i];
    final prefix = l.kind == _DKind.add
        ? '+ '
        : l.kind == _DKind.del
            ? '- '
            : '  ';
    final color = l.kind == _DKind.add
        ? _green
        : l.kind == _DKind.del
            ? _red
            : AppColors.text;
    final nl = i == lines.length - 1 ? '' : '\n';
    spans.add(TextSpan(text: '$prefix${l.text}$nl', style: TextStyle(color: color)));
  }
  // Long-press copies the resulting ("after") text: context + additions, which
  // is what you usually want to grab from a diff.
  final after = lines
      .where((l) => l.kind != _DKind.del)
      .map((l) => l.text)
      .join('\n');
  return CopyOnLongPress(
    text: after,
    child: Container(
      width: double.infinity,
      margin: const EdgeInsets.only(bottom: 6),
      padding: const EdgeInsets.all(8),
      decoration: BoxDecoration(
        color: AppColors.card,
        border: Border.all(color: AppColors.border),
        borderRadius: BorderRadius.circular(4),
      ),
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        child: Text.rich(TextSpan(children: spans, style: _mono)),
      ),
    ),
  );
}

/// Line-level LCS diff: context lines are plain, removals red, additions green.
/// Mirrors the TUI's lineDiff, including the >2000-line block fallback.
List<_DLine> _lineDiff(String oldS, String newS) {
  final a = _split(oldS), b = _split(newS);
  final n = a.length, m = b.length;
  if (n + m > 2000) {
    return [
      for (final l in a) _DLine(l, _DKind.del),
      for (final l in b) _DLine(l, _DKind.add),
    ];
  }
  final dp = List.generate(n + 1, (_) => List<int>.filled(m + 1, 0));
  for (var i = n - 1; i >= 0; i--) {
    for (var j = m - 1; j >= 0; j--) {
      dp[i][j] = a[i] == b[j]
          ? dp[i + 1][j + 1] + 1
          : (dp[i + 1][j] >= dp[i][j + 1] ? dp[i + 1][j] : dp[i][j + 1]);
    }
  }
  final out = <_DLine>[];
  var i = 0, j = 0;
  while (i < n && j < m) {
    if (a[i] == b[j]) {
      out.add(_DLine(a[i], _DKind.context));
      i++;
      j++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      out.add(_DLine(a[i], _DKind.del));
      i++;
    } else {
      out.add(_DLine(b[j], _DKind.add));
      j++;
    }
  }
  for (; i < n; i++) {
    out.add(_DLine(a[i], _DKind.del));
  }
  for (; j < m; j++) {
    out.add(_DLine(b[j], _DKind.add));
  }
  return out;
}

/// Splits into lines, dropping a single trailing newline. Empty input yields no
/// lines (so an all-additions block has no spurious blank context line).
List<String> _split(String s) {
  if (s.isEmpty) return const [];
  final t = s.endsWith('\n') ? s.substring(0, s.length - 1) : s;
  return t.split('\n');
}
