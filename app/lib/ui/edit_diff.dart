import 'dart:convert';

import 'package:flutter/material.dart';

import '../models/chunk.dart';
import 'code_block.dart';
import 'theme.dart';

const _red = Color(0xFFfb4934);
const _green = Color(0xFFb8bb26);
const _mono = TextStyle(fontFamily: 'monospace', fontSize: 12, height: 1.35);

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
      blocks.add(diffView(toolInputStr(input['old_string']),
          toolInputStr(input['new_string']),
          lang: path));
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
        blocks.add(diffView(
            toolInputStr(e['old_string']), toolInputStr(e['new_string']),
            lang: path));
      }
      break;
    case 'Write':
      blocks.add(diffView('', toolInputStr(input['content']), lang: path));
      break;
    case 'NotebookEdit':
      blocks.add(diffView('', toolInputStr(input['new_source']), lang: path));
      break;
    default:
      blocks.add(diffView('', item.toolInput ?? ''));
  }

  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: blocks);
}

/// Unchanged context kept on each side of a change; longer runs are folded.
const _ctxLines = 3;

/// Shortest hidden run worth folding — one or two lines behind a bar just adds taps.
const _foldThreshold = 4;

/// A collapsible unified diff for full-file review (PR style), unchanged runs
/// folded behind expand bars.
///
/// Place it in a bounded-height parent (e.g. a [Flexible]): it sizes to its
/// content but caps at the granted height, pinning its header and scrolling the
/// rows on overflow.
Widget collapsibleDiffView(String oldS, String newS, {String? lang}) =>
    _CollapsibleDiff(_lineDiff(oldS, newS, lang: langFromPath(lang)));

class _CollapsibleDiff extends StatefulWidget {
  const _CollapsibleDiff(this.lines);
  final List<_DLine> lines;

  @override
  State<_CollapsibleDiff> createState() => _CollapsibleDiffState();
}

class _CollapsibleDiffState extends State<_CollapsibleDiff> {
  final Set<int> _expanded = {}; // line indices in fold regions the user opened
  bool _expandAll = false;
  bool _wrap = false;
  bool _highlight = true;

  final _AnchoredScrollController _vScroll = _AnchoredScrollController();
  // Keys per rendered row and on the content column, so an expand can measure a
  // boundary row's offset before/after and shift the viewport by the real delta.
  final Map<int, GlobalKey> _rowKeys = {};
  final GlobalKey _contentKey = GlobalKey();

  late final List<int> _newNo; // new-side line number per row (0 for deletions)
  late final List<bool> _keep; // within _ctxLines of a change → never folded
  late final double _gutterWidth;

  @override
  void dispose() {
    _vScroll.dispose();
    super.dispose();
  }

  GlobalKey _keyFor(int i) => _rowKeys.putIfAbsent(i, () => GlobalKey());

  // A row's top relative to the content column, independent of scroll; null if
  // not measurable.
  double? _rowOffsetInContent(int index) {
    final row = _rowKeys[index]?.currentContext?.findRenderObject();
    final content = _contentKey.currentContext?.findRenderObject();
    if (row is RenderBox && row.attached && content is RenderBox &&
        content.attached) {
      return row.localToGlobal(Offset.zero, ancestor: content).dy;
    }
    return null;
  }

  /// Reveals a folded run. A leading run (file head, no row above) fills upward,
  /// so we hold the row below the bar in place by correcting the scroll during
  /// layout (before paint, no flicker). Middle/trailing runs fill downward — the
  /// natural insert — and need no correction.
  void _expandRegion(int start, int end) {
    if (start == 0 && end < widget.lines.length && _vScroll.hasClients) {
      final before = _rowOffsetInContent(end);
      if (before != null) {
        // Distance from the viewport top to the boundary row, to be preserved.
        final keep = before - _vScroll.offset;
        _vScroll.anchorNextLayout(() {
          final after = _rowOffsetInContent(end);
          return after == null ? null : after - keep;
        });
      }
    }
    setState(() {
      for (var k = start; k < end; k++) {
        _expanded.add(k);
      }
    });
  }

  @override
  void initState() {
    super.initState();
    final lines = widget.lines;
    _newNo = List<int>.filled(lines.length, 0);
    var nn = 0;
    for (var i = 0; i < lines.length; i++) {
      if (lines[i].kind != _DKind.del) _newNo[i] = ++nn;
    }
    _gutterWidth = nn.toString().length * 9.0 + 4;

    _keep = List<bool>.filled(lines.length, false);
    for (var i = 0; i < lines.length; i++) {
      if (lines[i].kind != _DKind.context) {
        for (var k = i - _ctxLines; k <= i + _ctxLines; k++) {
          if (k >= 0 && k < lines.length) _keep[k] = true;
        }
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final lines = widget.lines;
    if (lines.isEmpty) return const SizedBox.shrink();

    final rows = <Widget>[];
    var i = 0;
    while (i < lines.length) {
      if (!_keep[i] && !_expandAll && !_expanded.contains(i)) {
        var j = i;
        while (j < lines.length && !_keep[j] && !_expanded.contains(j)) {
          j++;
        }
        final count = j - i;
        if (count >= _foldThreshold) {
          final start = i, end = j;
          rows.add(_expandBar(start, end, count, () => _expandRegion(start, end)));
          i = j;
          continue;
        }
      }
      rows.add(KeyedSubtree(
        key: _keyFor(i),
        child: _diffLineRow(lines[i], _newNo[i], _gutterWidth, _wrap, _highlight),
      ));
      i++;
    }

    final content = Column(
        key: _contentKey,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: rows);

    // Vertical scroll always; horizontal too when not wrapping.
    final Widget scroller = _wrap
        ? SingleChildScrollView(controller: _vScroll, child: content)
        : SingleChildScrollView(
            controller: _vScroll,
            child: SingleChildScrollView(
              scrollDirection: Axis.horizontal,
              child: IntrinsicWidth(child: content),
            ),
          );

    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(bottom: 6),
      decoration: BoxDecoration(
        color: AppColors.card,
        border: Border.all(color: AppColors.border),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Column(
        // min so the box shrinks to its content; the Flexible below caps it.
        mainAxisSize: MainAxisSize.min,
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
                      style: _mono.copyWith(color: AppColors.dim, fontSize: 11)),
                ),
                codeBarButton(
                  icon: _expandAll ? Icons.unfold_less : Icons.unfold_more,
                  active: _expandAll,
                  tooltip: _expandAll ? 'Collapse unchanged' : 'Expand all',
                  onTap: () => setState(() {
                    _expandAll = !_expandAll;
                    if (!_expandAll) _expanded.clear();
                  }),
                ),
                codeBarButton(
                  icon: Icons.format_color_reset,
                  active: !_highlight,
                  tooltip: _highlight ? 'Disable highlight' : 'Enable highlight',
                  onTap: () => setState(() => _highlight = !_highlight),
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
          Flexible(
            child: Padding(
              padding: const EdgeInsets.all(8),
              child: scroller,
            ),
          ),
        ],
      ),
    );
  }

  // Expand bar for the folded run [start, end): the gutter icon points up at the
  // file's head, down at its tail, both ways for a run between two changes.
  Widget _expandBar(int start, int end, int count, VoidCallback onTap) {
    final IconData icon;
    if (start == 0) {
      icon = Icons.keyboard_double_arrow_up;
    } else if (end == widget.lines.length) {
      icon = Icons.keyboard_double_arrow_down;
    } else {
      icon = Icons.height;
    }
    const color = Color(0xFF83a598);
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 4),
        child: Row(
          children: [
            SizedBox(
              width: _gutterWidth,
              child: Align(
                alignment: Alignment.centerRight,
                child: Icon(icon, size: 16, color: color),
              ),
            ),
            const SizedBox(width: 8),
            Text('Expand $count unchanged lines',
                style: _mono.copyWith(color: color)),
          ],
        ),
      ),
    );
  }
}

enum _DKind { context, add, del }

class _DLine {
  const _DLine(this.text, this.kind, {this.runs, this.noEol = false});
  final String text;
  final _DKind kind;

  /// Syntax-highlighted runs for [text] (no prefix), or null to render flat.
  final List<CodeRun>? runs;

  /// The line was the last of its side with no trailing newline; the renderer
  /// shows a "\ No newline at end of file" marker beneath it.
  final bool noEol;
}

const _noEolLabel = r'\ No newline at end of file';

({String prefix, Color color}) _diffLineStyle(_DKind kind) => switch (kind) {
      _DKind.add => (prefix: '+ ', color: _green),
      _DKind.del => (prefix: '- ', color: _red),
      _DKind.context => (prefix: '  ', color: AppColors.text),
    };

/// Faint background tint marking add/removed lines; null for context.
Color? _diffLineBg(_DKind kind) => switch (kind) {
      _DKind.add => _green.withValues(alpha: 0.10),
      _DKind.del => _red.withValues(alpha: 0.10),
      _DKind.context => null,
    };

/// Spans for one diff line: the colored `+`/`-`/space prefix, then either
/// syntax-highlighted runs or a single flat-colored span.
List<TextSpan> _diffLineSpans(_DLine l, bool highlight) {
  final s = _diffLineStyle(l.kind);
  final runs = l.runs;
  if (highlight && runs != null) {
    return [
      TextSpan(text: s.prefix, style: TextStyle(color: s.color)),
      for (final r in runs) TextSpan(text: r.text, style: r.style),
    ];
  }
  return [
    TextSpan(text: '${s.prefix}${l.text}', style: TextStyle(color: s.color)),
  ];
}

/// A numbered diff row: new-side line number (blank for deletions) in a
/// [gutterWidth]-wide gutter, then the styled line text.
Widget _diffLineRow(
    _DLine l, int newNo, double gutterWidth, bool wrap, bool highlight) {
  final text = Text.rich(TextSpan(children: _diffLineSpans(l, highlight)),
      style: _mono, softWrap: wrap);
  final row = Row(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      SizedBox(
        width: gutterWidth,
        child: Text(l.kind == _DKind.del ? '' : '$newNo',
            textAlign: TextAlign.right,
            style: _mono.copyWith(color: AppColors.dim)),
      ),
      const SizedBox(width: 8),
      wrap ? Expanded(child: text) : text,
    ],
  );
  final bg = highlight ? _diffLineBg(l.kind) : null;
  final styled = bg == null ? row : ColoredBox(color: bg, child: row);
  if (!l.noEol) return styled;
  return Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [styled, _noEolMarkerRow(gutterWidth)],
  );
}

/// The no-newline marker, aligned under the line text and dimmed.
Widget _noEolMarkerRow(double gutterWidth) => Padding(
      padding: EdgeInsets.only(left: gutterWidth + 8),
      child: Text(_noEolLabel,
          style: _mono.copyWith(
              color: AppColors.dim, fontStyle: FontStyle.italic)),
    );

/// Empty [oldS] yields an all-additions view. [lang] hints the highlight grammar.
Widget diffView(String oldS, String newS, {String? lang}) =>
    _DiffBox(_lineDiff(oldS, newS, lang: langFromPath(lang)));

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
  bool _highlight = true;

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
                  icon: Icons.format_color_reset,
                  active: !_highlight,
                  tooltip: _highlight ? 'Disable highlight' : 'Enable highlight',
                  onTap: () => setState(() => _highlight = !_highlight),
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
                    scrollDirection: Axis.horizontal,
                    child: IntrinsicWidth(child: content)),
          ),
        ],
      ),
    );
  }

  // One paragraph of all lines, with a text-background tint (not full-row, which
  // would need per-row layout) on add/removed lines.
  Widget _plain() {
    final spans = <TextSpan>[];
    for (var i = 0; i < widget.lines.length; i++) {
      final l = widget.lines[i];
      final bg = _highlight ? _diffLineBg(l.kind) : null;
      for (final span in _diffLineSpans(l, _highlight)) {
        spans.add(bg == null
            ? span
            : TextSpan(
                text: span.text,
                style: (span.style ?? const TextStyle())
                    .copyWith(backgroundColor: bg)));
      }
      if (l.noEol) {
        spans.add(const TextSpan(
            text: '\n$_noEolLabel',
            style: TextStyle(
                color: AppColors.dim, fontStyle: FontStyle.italic)));
      }
      if (i != widget.lines.length - 1) spans.add(const TextSpan(text: '\n'));
    }
    return Text.rich(TextSpan(children: spans, style: _mono), softWrap: _wrap);
  }

  // Numbers the new side; deletions get a blank gutter. Starts at 1 (no offset).
  Widget _numbered() {
    final newCount = widget.lines.where((l) => l.kind != _DKind.del).length;
    final gutterWidth = newCount.toString().length * 9.0;
    final rows = <Widget>[];
    var newNo = 0;
    for (final l in widget.lines) {
      if (l.kind != _DKind.del) newNo++;
      rows.add(_diffLineRow(l, newNo, gutterWidth, _wrap, _highlight));
    }
    return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch, children: rows);
  }
}

/// Line-level LCS diff. Each side is syntax-highlighted once ([lang] hints the
/// grammar); the per-line runs then attach to their diff line — deletions from
/// the old side, additions and context from the new side.
List<_DLine> _lineDiff(String oldS, String newS, {String? lang}) {
  oldS = _normalizeEol(oldS);
  newS = _normalizeEol(newS);
  final a = _split(oldS), b = _split(newS);
  final n = a.length, m = b.length;

  // Match on trailing-newline state too, so adding/removing just the final
  // newline diffs the last line instead of vanishing into context.
  bool same(_SrcLine x, _SrcLine y) => x.text == y.text && x.eol == y.eol;

  // Large diffs skip the O(n·m) LCS and highlighting (two full-file tokenizer
  // passes would jank the UI isolate); the renderer falls back to flat color.
  if (n + m > 2000) {
    return [
      for (var i = 0; i < n; i++)
        _DLine(a[i].text, _DKind.del, noEol: !a[i].eol),
      for (var j = 0; j < m; j++)
        _DLine(b[j].text, _DKind.add, noEol: !b[j].eol),
    ];
  }

  final oldRuns = highlightLines(oldS, lang: lang);
  final newRuns = highlightLines(newS, lang: lang);
  // Runs may carry a trailing empty line that _split drops; guard the lookup.
  List<CodeRun>? runsAt(List<List<CodeRun>> runs, int idx) =>
      idx >= 0 && idx < runs.length ? runs[idx] : null;

  final dp = List.generate(n + 1, (_) => List<int>.filled(m + 1, 0));
  for (var i = n - 1; i >= 0; i--) {
    for (var j = m - 1; j >= 0; j--) {
      dp[i][j] = same(a[i], b[j])
          ? dp[i + 1][j + 1] + 1
          : (dp[i + 1][j] >= dp[i][j + 1] ? dp[i + 1][j] : dp[i][j + 1]);
    }
  }
  final out = <_DLine>[];
  var i = 0, j = 0;
  while (i < n && j < m) {
    if (same(a[i], b[j])) {
      out.add(_DLine(a[i].text, _DKind.context,
          runs: runsAt(newRuns, j), noEol: !a[i].eol));
      i++;
      j++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      out.add(_DLine(a[i].text, _DKind.del,
          runs: runsAt(oldRuns, i), noEol: !a[i].eol));
      i++;
    } else {
      out.add(_DLine(b[j].text, _DKind.add,
          runs: runsAt(newRuns, j), noEol: !b[j].eol));
      j++;
    }
  }
  for (; i < n; i++) {
    out.add(_DLine(a[i].text, _DKind.del,
        runs: runsAt(oldRuns, i), noEol: !a[i].eol));
  }
  for (; j < m; j++) {
    out.add(_DLine(b[j].text, _DKind.add,
        runs: runsAt(newRuns, j), noEol: !b[j].eol));
  }
  return out;
}

/// One source line plus whether it was newline-terminated — carried so the diff
/// can surface an otherwise-invisible trailing-newline-only change.
class _SrcLine {
  const _SrcLine(this.text, {required this.eol});
  final String text;
  final bool eol;
}

/// Collapses CRLF to LF so line endings never render as stray carriage returns
/// and a pure LF↔CRLF change doesn't diff every line.
String _normalizeEol(String s) => s.replaceAll('\r\n', '\n');

/// Splits into lines, dropping a single trailing newline but recording whether
/// it was present. Empty input yields no lines (no spurious blank context line).
List<_SrcLine> _split(String s) {
  if (s.isEmpty) return const [];
  final hasTrailing = s.endsWith('\n');
  final body = hasTrailing ? s.substring(0, s.length - 1) : s;
  final parts = body.split('\n');
  return [
    for (var k = 0; k < parts.length; k++)
      _SrcLine(parts[k], eol: k < parts.length - 1 || hasTrailing),
  ];
}

/// A scroll controller that applies a one-shot offset correction during the next
/// layout, before paint — so inserting rows above the viewport keeps the visible
/// content put without the one-frame flicker a post-frame `jumpTo` would cause.
class _AnchoredScrollController extends ScrollController {
  double? Function()? _pending;

  void anchorNextLayout(double? Function() computeTarget) =>
      _pending = computeTarget;

  double? Function()? _takePending() {
    final p = _pending;
    _pending = null;
    return p;
  }

  @override
  ScrollPosition createScrollPosition(ScrollPhysics physics,
          ScrollContext context, ScrollPosition? oldPosition) =>
      _AnchoredScrollPosition(
        physics: physics,
        context: context,
        oldPosition: oldPosition,
        takePending: _takePending,
      );
}

class _AnchoredScrollPosition extends ScrollPositionWithSingleContext {
  _AnchoredScrollPosition({
    required super.physics,
    required super.context,
    super.oldPosition,
    required this.takePending,
  });

  final double? Function()? Function() takePending;

  @override
  bool applyContentDimensions(double minScrollExtent, double maxScrollExtent) {
    final applied =
        super.applyContentDimensions(minScrollExtent, maxScrollExtent);
    final compute = takePending();
    final target = compute?.call();
    if (target != null) {
      final clamped = target.clamp(minScrollExtent, maxScrollExtent);
      if ((clamped - pixels).abs() > 0.5) {
        correctPixels(clamped);
        return false; // re-layout with the corrected offset before painting
      }
    }
    return applied;
  }
}
