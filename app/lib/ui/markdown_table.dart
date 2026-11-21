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
    total += w < floor ? w : floor;
  }
  return total <= available;
}

String stackedLabel(String header, int column) =>
    header.trim().isEmpty ? 'Column $column' : header.trim();
