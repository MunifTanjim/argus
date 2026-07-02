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
Widget _diff(String oldS, String newS) => _DiffBox(_lineDiff(oldS, newS));

/// A diff box with a thin header (label + wrap toggle), mirroring code blocks.
/// Copy is intentionally omitted — a diff is for reading, not grabbing text.
class _DiffBox extends StatefulWidget {
  const _DiffBox(this.lines);

  final List<_DLine> lines;

  @override
  State<_DiffBox> createState() => _DiffBoxState();
}

class _DiffBoxState extends State<_DiffBox> {
  bool _wrap = false;
  bool _lineNumbers = false;

  @override
  Widget build(BuildContext context) {
    if (widget.lines.isEmpty) return const SizedBox.shrink();
    final content = _lineNumbers ? _numbered() : _plain();
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(bottom: 6),
      decoration: BoxDecoration(
        color: AppColors.card,
        border: Border.all(color: AppColors.border),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Container(
            padding: const EdgeInsets.only(left: 8, right: 2),
            decoration: const BoxDecoration(
              border: Border(bottom: BorderSide(color: AppColors.border)),
            ),
            child: Row(
              children: [
                Expanded(
                  child: Text('diff',
                      style: _mono.copyWith(
                          color: AppColors.dim, fontSize: 11)),
                ),
                codeBarButton(
                  icon: Icons.format_list_numbered,
                  active: _lineNumbers,
                  tooltip:
                      _lineNumbers ? 'Hide line numbers' : 'Show line numbers',
                  onTap: () => setState(() => _lineNumbers = !_lineNumbers),
                ),
                codeBarButton(
                  icon: Icons.wrap_text,
                  active: _wrap,
                  tooltip: _wrap ? 'Disable wrap' : 'Wrap lines',
                  onTap: () => setState(() => _wrap = !_wrap),
                ),
              ],
            ),
          ),
          Padding(
            padding: const EdgeInsets.all(8),
            child: _wrap
                ? content
                : SingleChildScrollView(
                    scrollDirection: Axis.horizontal, child: content),
          ),
        ],
      ),
    );
  }

  ({String prefix, Color color}) _style(_DKind kind) => switch (kind) {
        _DKind.add => (prefix: '+ ', color: _green),
        _DKind.del => (prefix: '- ', color: _red),
        _DKind.context => (prefix: '  ', color: AppColors.text),
      };

  Widget _plain() {
    final spans = <TextSpan>[];
    for (var i = 0; i < widget.lines.length; i++) {
      final l = widget.lines[i];
      final s = _style(l.kind);
      final nl = i == widget.lines.length - 1 ? '' : '\n';
      spans.add(TextSpan(
          text: '${s.prefix}${l.text}$nl', style: TextStyle(color: s.color)));
    }
    return Text.rich(TextSpan(children: spans, style: _mono), softWrap: _wrap);
  }

  // Numbers the new-side (context + additions); deletions get a blank gutter.
  // No file offset is available, so numbering starts at 1.
  Widget _numbered() {
    final newCount =
        widget.lines.where((l) => l.kind != _DKind.del).length;
    final gutterWidth = newCount.toString().length * 9.0;
    final rows = <Widget>[];
    var newNo = 0;
    for (final l in widget.lines) {
      final isDel = l.kind == _DKind.del;
      if (!isDel) newNo++;
      final s = _style(l.kind);
      final text = Text('${s.prefix}${l.text}',
          style: _mono.copyWith(color: s.color), softWrap: _wrap);
      rows.add(Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: gutterWidth,
            child: Text(isDel ? '' : '$newNo',
                textAlign: TextAlign.right,
                style: _mono.copyWith(color: AppColors.dim)),
          ),
          const SizedBox(width: 8),
          _wrap ? Expanded(child: text) : text,
        ],
      ));
    }
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: rows);
  }
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
