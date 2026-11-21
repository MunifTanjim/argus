import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:gpt_markdown/custom_widgets/markdown_config.dart';
import 'package:gpt_markdown/gpt_markdown.dart';

/// The narrowest a column may shrink to, in characters, before a table stacks.
/// The TUI uses the same value; keep them in sync.
const tableMinColumnChars = 12;

const _cellPadding = 8.0;

final _inlineCode = RegExp(r'`([^`]*)`');
final _inlineLink = RegExp(r'\[([^\]]*)\]\([^)]*\)');
final _inlineEmph = [
  RegExp(r'\*\*(.+?)\*\*'),
  RegExp(r'__(.+?)__'),
  RegExp(r'~~(.+?)~~'),
  RegExp(r'\*(.+?)\*'),
  RegExp(r'\b_(.+?)_\b'),
];

/// Approximates the rendered text of a markdown cell, for width measurement.
String stripInlineMarkdown(String s) {
  String inner(Match m) => m[1]!;
  s = s.replaceAllMapped(_inlineCode, inner);
  s = s.replaceAllMapped(_inlineLink, inner);
  for (final re in _inlineEmph) {
    s = s.replaceAllMapped(re, inner);
  }
  return s.replaceAll(r'\|', '|');
}

/// Whether a table with [natural] column widths fits [available] when each
/// column may shrink to `min(natural, floor)`.
bool tableFitsGrid(List<double> natural, double floor, double available) {
  final cols = natural.length;
  var total = cols * 2 * _cellPadding + (cols + 1);
  for (final w in natural) {
    total += math.min(w, floor);
  }
  return total <= available;
}

String stackedLabel(String header, int column) {
  final label = stripInlineMarkdown(header.trim());
  return label.isEmpty ? 'Column $column' : label;
}

/// A [TableBuilder] that draws a grid when the table fits the width, and
/// stacked "header: cell" records when it does not.
Widget markdownTable(BuildContext context, List<CustomTableRow> rows,
    TextStyle style, GptMarkdownConfig config) {
  if (rows.isEmpty) return const SizedBox.shrink();
  // gpt_markdown pads every row, header included, to the widest row.
  final all = rows.first.fields;
  final cols = all.lastIndexWhere((f) => f.data.trim().isNotEmpty) + 1;
  final header = cols == 0 ? all : all.sublist(0, cols);
  final body = rows.skip(1).toList();
  final textStyle = DefaultTextStyle.of(context).style.merge(style);
  final scaler = MediaQuery.textScalerOf(context);
  double measure(String s) {
    final tp = TextPainter(
      text: TextSpan(text: s, style: textStyle),
      textDirection: TextDirection.ltr,
      textScaler: scaler,
    )..layout();
    final w = tp.width;
    tp.dispose();
    return w;
  }

  double cellWidth(CustomTableField f) =>
      measure(stripInlineMarkdown(f.data.trim()));

  final natural = List<double>.generate(header.length, (c) {
    var w = cellWidth(header[c]);
    for (final r in body) {
      if (c < r.fields.length) w = math.max(w, cellWidth(r.fields[c]));
    }
    return w;
  });
  final floor = measure('0' * tableMinColumnChars);

  return LayoutBuilder(builder: (context, constraints) {
    final available = constraints.maxWidth;
    if (header.length < 2 ||
        body.isEmpty ||
        tableFitsGrid(natural, floor, available)) {
      final fitsNaturally =
          tableFitsGrid(natural, double.infinity, available);
      return _grid(context, rows, natural,
          fitsNaturally ? double.infinity : floor, config);
    }
    return _stacked(context, header, body, config);
  });
}

Widget _grid(BuildContext context, List<CustomTableRow> rows,
    List<double> natural, double floor, GptMarkdownConfig config) {
  final scheme = Theme.of(context).colorScheme;
  return Table(
    columnWidths: {
      for (var c = 0; c < natural.length; c++)
        c: natural[c] <= floor
            ? const IntrinsicColumnWidth()
            : FlexColumnWidth(natural[c]),
    },
    defaultVerticalAlignment: TableCellVerticalAlignment.middle,
    border: TableBorder.all(width: 1, color: scheme.onSurface),
    children: [
      for (final r in rows)
        TableRow(
          decoration: r.isHeader
              ? BoxDecoration(color: scheme.surfaceContainerHighest)
              : null,
          children: [
            for (final f in r.fields.take(natural.length))
              Align(
                alignment: switch (f.alignment) {
                  TextAlign.center => Alignment.center,
                  TextAlign.right => Alignment.centerRight,
                  _ => Alignment.centerLeft,
                },
                child: Padding(
                  padding: const EdgeInsets.symmetric(
                      horizontal: _cellPadding, vertical: 4),
                  child:
                      MdWidget(context, f.data.trim(), false, config: config),
                ),
              ),
          ],
        ),
    ],
  );
}

Widget _stacked(BuildContext context, List<CustomTableField> header,
    List<CustomTableRow> body, GptMarkdownConfig config) {
  final labels = [
    for (var c = 0; c < header.length; c++) stackedLabel(header[c].data, c + 1)
  ];
  final records = <Widget>[];
  for (final r in body) {
    final lines = <Widget>[
      for (var c = 0; c < labels.length && c < r.fields.length; c++)
        if (r.fields[c].data.trim() case final v when v.isNotEmpty)
          MdWidget(context, '**${labels[c]}:** $v', false, config: config),
    ];
    if (lines.isEmpty) continue;
    records.add(const Divider());
    records.add(Column(
        crossAxisAlignment: CrossAxisAlignment.start, children: lines));
  }
  if (records.isEmpty) return const SizedBox.shrink();
  return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [...records, const Divider()]);
}
