import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:gpt_markdown/gpt_markdown.dart';
import 'package:re_highlight/languages/all.dart';
import 'package:re_highlight/re_highlight.dart';
import 'package:re_highlight/styles/all.dart';
import 'package:url_launcher/url_launcher.dart';

import 'theme.dart';

/// A backtick fence guaranteed longer than any backtick run inside [body], so an
/// embedded ``` cannot terminate the fenced block early.
String safeFence(String body) {
  var longest = 0, run = 0;
  for (final cu in body.codeUnits) {
    if (cu == 0x60) {
      run++;
      if (run > longest) longest = run;
    } else {
      run = 0;
    }
  }
  final n = longest + 1 > 3 ? longest + 1 : 3;
  return '`' * n;
}

/// Renders markdown with syntax-highlighted code blocks. Fenced code renders
/// with `selectable: false` to avoid a forbidden nested SelectionArea. The
/// toolbar's "Copy Markdown" action copies raw source since native Copy yields
/// rendered text with the markup stripped.
Widget appMarkdown(String data) => _SelectableMarkdown(data);

class _SelectableMarkdown extends StatefulWidget {
  const _SelectableMarkdown(this.data);
  final String data;
  @override
  State<_SelectableMarkdown> createState() => _SelectableMarkdownState();
}

class _SelectableMarkdownState extends State<_SelectableMarkdown> {
  // Mutated without setState — read only when "Copy Markdown" fires.
  String? _selected;

  @override
  Widget build(BuildContext context) => SelectionArea(
        onSelectionChanged: (c) => _selected = c?.plainText,
        contextMenuBuilder: (_, state) =>
            AdaptiveTextSelectionToolbar.buttonItems(
          anchors: state.contextMenuAnchors,
          buttonItems: [
            ...state.contextMenuButtonItems,
            ContextMenuButtonItem(
              label: 'Copy Markdown',
              onPressed: () {
                final md = extractMarkdown(widget.data, _selected);
                if (md == null) {
                  _showSnack(context, "Couldn't copy markdown source");
                } else {
                  copyToClipboard(context, md);
                }
                state.hideToolbar();
                state.clearSelection();
              },
            ),
          ],
        ),
        child: Builder(
          builder: (context) => GptMarkdown(
            widget.data,
            onLinkTap: (url, title) => showLinkActions(context, url),
            codeBuilder: (context, name, code, closed) =>
                codeView(code, lang: name, selectable: false),
          ),
        ),
      );
}

/// Maps a rendered [selected] range back to the source lines it spans, returned
/// verbatim; null when it can't be anchored. The render is flattened text with
/// markup stripped, so matching is line-granular, not char-precise.
///
/// Matching is coarse: wide table selections fall back to the whole block.
/// Per-line reconstruction would tighten it, if that ever matters.
String? extractMarkdown(String source, String? selected) {
  final sel = _collapse(selected ?? '');
  if (sel.isEmpty) return null;
  final lines = source.split('\n');
  final range = _inferMarkdownRange(lines, sel);
  if (range == null) return null;
  return lines.sublist(range.$1, range.$2 + 1).join('\n');
}

final _wsRe = RegExp(r'\s+');
final _blockquoteRe = RegExp(r'^\s*(>\s?)+');
final _headingRe = RegExp(r'^\s*#{1,6}\s+');
final _listMarkerRe = RegExp(r'^\s*([-*+]|\d+[.)])\s+');
final _taskBoxRe = RegExp(r'^\s*\[[ xX]\]\s+');
final _linkRe = RegExp(r'!?\[([^\]]*)\]\([^)]*\)');
final _fenceRe = RegExp(r'^\s*(`{3,}|~{3,})');
final _inlineCodeRe = RegExp(r'`[^`]+`');

String _collapse(String s) => s.replaceAll(_wsRe, ' ').trim().toLowerCase();

/// Strips markdown markers from a source [line] so it reads like its rendered
/// text. Inline-code spans are left intact (backticks aside): their `* _ |`
/// render literally, so stripping them would break the match.
String _normalizeMdLine(String line) {
  var s = line;
  s = s.replaceFirst(_blockquoteRe, '');
  s = s.replaceFirst(_headingRe, '');
  s = s.replaceFirst(_listMarkerRe, '');
  s = s.replaceFirst(_taskBoxRe, '');

  final out = StringBuffer();
  var i = 0;
  for (final m in _inlineCodeRe.allMatches(s)) {
    out.write(_stripInlineMarkers(s.substring(i, m.start)));
    out.write(m[0]!.substring(1, m[0]!.length - 1));
    i = m.end;
  }
  out.write(_stripInlineMarkers(s.substring(i)));
  return _collapse(out.toString());
}

String _stripInlineMarkers(String s) => s
    .replaceAllMapped(_linkRe, (m) => m[1] ?? '')
    .replaceAll('**', '')
    .replaceAll('__', '')
    .replaceAll('~~', '')
    .replaceAll('*', '')
    .replaceAll('_', '')
    .replaceAll('`', '')
    .replaceAll('|', ' ');

/// Anchors [sel] to an inclusive source line range, or null when unmatched.
///
/// Grows a contiguous run downward from the first matching line while the join
/// of normalized lines stays a prefix of [sel]. Staying contiguous is what keeps
/// it from jumping to a distant duplicate line.
(int, int)? _inferMarkdownRange(List<String> lines, String sel) {
  final norm = [for (final l in lines) _normalizeMdLine(l)];
  final lead = sel.substring(0, sel.length < 24 ? sel.length : 24);

  var start = -1;
  for (var i = 0; i < norm.length; i++) {
    if (norm[i].isEmpty) continue;
    if (sel.startsWith(norm[i]) || norm[i].contains(lead)) {
      start = i;
      break;
    }
  }
  if (start == -1) return null;

  // The selection may begin mid-line, so start from where [lead] appears.
  final p = norm[start].indexOf(lead);
  var acc = p > 0 ? norm[start].substring(p) : norm[start];

  var end = start;
  if (!acc.startsWith(sel)) {
    for (var j = start + 1; j < norm.length; j++) {
      if (norm[j].isEmpty) continue;
      final cand = '$acc ${norm[j]}';
      if (sel.startsWith(cand)) {
        acc = cand;
        end = j;
        if (acc.length >= sel.length) break; // whole selection consumed
      } else if (cand.startsWith(sel)) {
        end = j; // selection ends partway through this line
        break;
      } else {
        break;
      }
    }
  }
  return _expandToConstructs(lines, start, end);
}

/// Grows [start]/[end] outward to fully cover any table or fenced-code block an
/// anchor landed inside, so those are never copied half-cut.
(int, int) _expandToConstructs(List<String> lines, int start, int end) {
  // Single pass is safe because fence and table ranges are disjoint; a
  // fixed-point loop would only be needed if overlapping constructs appear.
  for (final (lo, hi) in [..._fenceRanges(lines), ..._tableRanges(lines)]) {
    if (start > lo && start <= hi) start = lo;
    if (end < hi && end >= lo) end = hi;
  }
  return (start, end);
}

/// Inclusive line ranges of fenced code blocks (``` or ~~~, 3+). An unclosed
/// fence runs to the last line.
List<(int, int)> _fenceRanges(List<String> lines) {
  final out = <(int, int)>[];
  int? open;
  for (var i = 0; i < lines.length; i++) {
    if (!_fenceRe.hasMatch(lines[i])) continue;
    if (open == null) {
      open = i;
    } else {
      out.add((open, i));
      open = null;
    }
  }
  if (open != null) out.add((open, lines.length - 1));
  return out;
}

/// Inclusive line ranges of pipe tables (2+ contiguous lines starting with `|`).
List<(int, int)> _tableRanges(List<String> lines) {
  bool isRow(int k) => lines[k].trimLeft().startsWith('|');
  final out = <(int, int)>[];
  var i = 0;
  while (i < lines.length) {
    if (isRow(i)) {
      final s = i;
      while (i < lines.length && isRow(i)) {
        i++;
      }
      if (i - s >= 2) out.add((s, i - 1));
    } else {
      i++;
    }
  }
  return out;
}

/// Renders [body] as a standalone code block (see [codeView]).
Widget codeBlock(String body,
        {String? lang,
        bool lineNumberToggle = true,
        bool wrap = false}) =>
    codeView(body,
        lang: lang, lineNumberToggle: lineNumberToggle, wrap: wrap);

/// Renders [body] as a syntax-highlighted code block via re_highlight
/// (highlight.js grammars). JSON is pretty-printed first. A thin header carries
/// the language label plus line-number, wrap, and copy buttons.
///
/// Set [lineNumberToggle] false for content that already carries its own line
/// numbers (e.g. the Read tool's `cat -n` output). [wrap] sets the initial line
/// wrap state (still user-toggleable). [selectable] controls whether the content
/// region is wrapped in a [SelectionArea]; set to `false` when an ancestor
/// [SelectionArea] already covers this block (e.g. inside [appMarkdown]).
Widget codeView(String body,
    {String? lang,
    bool lineNumberToggle = true,
    bool wrap = false,
    bool selectable = true}) {
  // Show the box with a dim marker so an empty block still reads as "a block that
  // was empty" rather than a missing render.
  if (body.trim().isEmpty) {
    return _container(
        Text('(empty)', style: _codeMono.copyWith(color: AppColors.dim)),
        selectable: selectable);
  }
  return _CodeBlock(
      body: body,
      lang: lang,
      lineNumberToggle: lineNumberToggle,
      wrap: wrap,
      selectable: selectable);
}

class _CodeBlock extends StatefulWidget {
  const _CodeBlock(
      {required this.body,
      this.lang,
      this.lineNumberToggle = true,
      this.wrap = false,
      this.selectable = true});

  final String body;
  final String? lang;
  final bool lineNumberToggle;
  final bool wrap;
  final bool selectable;

  @override
  State<_CodeBlock> createState() => _CodeBlockState();
}

class _CodeBlockState extends State<_CodeBlock> {
  late bool _wrap = widget.wrap;
  bool _lineNumbers = false;
  bool _plain = false;

  @override
  Widget build(BuildContext context) {
    final pretty = _prettyJson(widget.body);
    final source = pretty ?? widget.body;
    // An unknown fence language (or any highlighter hiccup) must not break the
    // block — fall back to plain monospace.
    TextSpan span;
    String? detected;
    if (_plain) {
      span = TextSpan(text: source, style: _codeMono);
    } else {
      try {
        final result = _highlight.highlightAuto(
            source, _candidates(pretty != null ? 'json' : widget.lang));
        detected = result.language;
        final renderer = TextSpanRenderer(_codeMono, _codeTheme);
        result.render(renderer);
        span = renderer.span ?? TextSpan(text: source, style: _codeMono);
      } catch (_) {
        span = TextSpan(text: source, style: _codeMono);
      }
    }
    final label =
        (pretty != null ? 'json' : _alias(widget.lang)) ?? detected ?? 'text';

    // Without a gutter, the whole block is one paragraph. With one, each logical
    // line becomes its own row so its number stays aligned even when wrapped.
    final Widget content = _lineNumbers
        ? _numbered(_spanLines(span))
        : Text.rich(span, softWrap: _wrap);
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.symmetric(vertical: 4),
      decoration: BoxDecoration(
        color: AppColors.canvas,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: AppColors.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _header(label),
          Padding(
            padding: const EdgeInsets.all(10),
            child: widget.selectable
                ? SelectionArea(child: _scrollable(content))
                : _scrollable(content),
          ),
        ],
      ),
    );
  }

  Widget _scrollable(Widget content) => _wrap
      ? content
      : SingleChildScrollView(
          scrollDirection: Axis.horizontal, child: content);

  Widget _numbered(List<List<CodeRun>> lines) {
    final gutterWidth = lines.length.toString().length * 9.0;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        for (var i = 0; i < lines.length; i++)
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              SizedBox(
                width: gutterWidth,
                child: Text('${i + 1}',
                    textAlign: TextAlign.right,
                    style: _codeMono.copyWith(color: AppColors.dim)),
              ),
              const SizedBox(width: 8),
              // In wrap mode the line needs a bounded width to wrap into; when
              // scrolling it must keep its intrinsic (overflowing) width.
              _wrap ? Expanded(child: _lineText(lines[i])) : _lineText(lines[i]),
            ],
          ),
      ],
    );
  }

  Widget _lineText(List<CodeRun> runs) => Text.rich(
        TextSpan(
          style: _codeMono,
          // A blank line still needs a glyph so its row keeps the line height.
          children: runs.isEmpty
              ? const [TextSpan(text: ' ')]
              : [for (final r in runs) TextSpan(text: r.text, style: r.style)],
        ),
        softWrap: _wrap,
      );

  Widget _header(String label) => Container(
        padding: const EdgeInsets.only(left: 10, right: 2),
        decoration: const BoxDecoration(
          border: Border(bottom: BorderSide(color: AppColors.border)),
        ),
        child: Row(
          children: [
            Expanded(
              child: Text(label,
                  style:
                      _codeMono.copyWith(color: AppColors.dim, fontSize: 11)),
            ),
            if (widget.lineNumberToggle)
              codeBarButton(
                icon: Icons.format_list_numbered,
                active: _lineNumbers,
                tooltip:
                    _lineNumbers ? 'Hide line numbers' : 'Show line numbers',
                onTap: () => setState(() => _lineNumbers = !_lineNumbers),
              ),
            codeBarButton(
              icon: Icons.format_color_reset,
              active: _plain,
              tooltip: _plain ? 'Enable highlight' : 'Disable highlight',
              onTap: () => setState(() => _plain = !_plain),
            ),
            codeBarButton(
              icon: Icons.wrap_text,
              active: _wrap,
              tooltip: _wrap ? 'Disable wrap' : 'Wrap lines',
              onTap: () => setState(() => _wrap = !_wrap),
            ),
            codeBarButton(
              icon: Icons.copy,
              tooltip: 'Copy',
              onTap: () => copyToClipboard(context, widget.body),
            ),
          ],
        ),
      );
}

/// A styled text run within one line, from the flattened highlight tree.
class CodeRun {
  const CodeRun(this.text, this.style);
  final String text;
  final TextStyle? style;
}

/// Highlights [source] into per-line styled runs aligned to `source.split('\n')`,
/// falling back to one unstyled run per line on any highlighter error.
List<List<CodeRun>> highlightLines(String source, {String? lang}) {
  List<List<CodeRun>> plain() => [
        for (final l in source.split('\n')) [if (l.isNotEmpty) CodeRun(l, null)]
      ];
  if (source.isEmpty) return const <List<CodeRun>>[];
  try {
    final result = _highlight.highlightAuto(source, _candidates(lang));
    final renderer = TextSpanRenderer(_codeMono, _codeTheme);
    result.render(renderer);
    final span = renderer.span;
    if (span == null) return plain();
    return _spanLines(span);
  } catch (_) {
    return plain();
  }
}

/// Flattens a highlighted [span] tree into per-line runs so a line-number gutter
/// can align to each logical line. Text renders before children within a span,
/// so the walk order matches on-screen order.
List<List<CodeRun>> _spanLines(InlineSpan span) {
  final lines = <List<CodeRun>>[<CodeRun>[]];
  void walk(InlineSpan s, TextStyle? inherited) {
    if (s is! TextSpan) return;
    final style = inherited == null ? s.style : inherited.merge(s.style);
    final text = s.text;
    if (text != null) {
      final parts = text.split('\n');
      for (var i = 0; i < parts.length; i++) {
        if (i > 0) lines.add(<CodeRun>[]);
        if (parts[i].isNotEmpty) lines.last.add(CodeRun(parts[i], style));
      }
    }
    for (final c in s.children ?? const <InlineSpan>[]) {
      walk(c, style);
    }
  }

  walk(span, null);
  return lines;
}

/// Compact icon button for code/diff header bars and inline copy affordances.
Widget codeBarButton({
  required IconData icon,
  required String tooltip,
  required VoidCallback onTap,
  bool active = false,
}) =>
    IconButton(
      icon: Icon(icon, size: 16),
      color: active ? AppColors.accent : AppColors.dim,
      tooltip: tooltip,
      visualDensity: VisualDensity.compact,
      padding: const EdgeInsets.all(6),
      constraints: const BoxConstraints(),
      onPressed: onTap,
    );

/// Presents a bottom sheet offering to open [url] externally or copy it, so a
/// tapped link isn't launched without confirmation.
void showLinkActions(BuildContext context, String url) {
  showModalBottomSheet<void>(
    context: context,
    builder: (sheetCtx) {
      ListTile action(IconData icon, String label, VoidCallback onTap) =>
          ListTile(
            leading: Icon(icon),
            title: Text(label),
            onTap: () {
              Navigator.pop(sheetCtx);
              onTap();
            },
          );
      return SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
              child: Text(url,
                  style: const TextStyle(color: AppColors.dim, fontSize: 13),
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis),
            ),
            action(Icons.open_in_new, 'Open link', () => openExternalUrl(url)),
            action(Icons.copy, 'Copy link', () => copyToClipboard(context, url)),
          ],
        ),
      );
    },
  );
}

/// Opens [url] in the external browser. Overridable in tests. Malformed or
/// unlaunchable URLs are ignored so a bad link can never crash the render.
Future<void> Function(String url) openExternalUrl = _openExternalUrl;

Future<void> _openExternalUrl(String url) async {
  final uri = Uri.tryParse(url);
  if (uri == null) return;
  try {
    await launchUrl(uri, mode: LaunchMode.externalApplication);
  } catch (_) {
    // Unlaunchable URL (no handler, platform error): ignore.
  }
}

/// Copies [text] to the clipboard and shows a brief confirmation snackbar when a
/// messenger is available.
void copyToClipboard(BuildContext context, String text) {
  Clipboard.setData(ClipboardData(text: text));
  _showSnack(context, 'Copied');
}

/// Shows a brief snackbar with [message] when a messenger is available.
void _showSnack(BuildContext context, String message) {
  ScaffoldMessenger.maybeOf(context)?.showSnackBar(SnackBar(
    content: Text(message),
    duration: const Duration(seconds: 1),
  ));
}

Widget _container(Widget child, {bool selectable = true}) {
  final inner =
      SingleChildScrollView(scrollDirection: Axis.horizontal, child: child);
  return Container(
    width: double.infinity,
    margin: const EdgeInsets.symmetric(vertical: 4),
    padding: const EdgeInsets.all(10),
    decoration: BoxDecoration(
      color: AppColors.canvas,
      borderRadius: BorderRadius.circular(6),
      border: Border.all(color: AppColors.border),
    ),
    child: selectable ? SelectionArea(child: inner) : inner,
  );
}

// Shared highlighter with all highlight.js grammars registered.
final Highlight _highlight = Highlight()
  ..registerLanguages(builtinAllLanguages);

// Gruvbox-dark theme to match the app palette, with a safe fallback.
final Map<String, TextStyle> _codeTheme = _resolveTheme();

Map<String, TextStyle> _resolveTheme() {
  final t = builtinAllThemes['base16-gruvbox-dark-medium'] ??
      builtinAllThemes['atom-one-dark'];
  return t is Map<String, TextStyle> ? t : const <String, TextStyle>{};
}

const _codeMono = TextStyle(
    fontFamily: 'monospace', fontSize: 12, height: 1.35, color: AppColors.text);

/// Candidate languages for highlightAuto. A known fence language narrows it to
/// one grammar; otherwise a small set covering the languages we see most.
List<String> _candidates(String? lang) {
  final l = _alias(lang);
  if (l != null) return [l];
  return const [
    'json',
    'dart',
    'go',
    'bash',
    'yaml',
    'javascript',
    'typescript',
    'python',
  ];
}

String? _alias(String? name) {
  final n = name?.trim().toLowerCase();
  if (n == null || n.isEmpty) return null;
  switch (n) {
    case 'sh':
    case 'shell':
    case 'console':
    case 'zsh':
      return 'bash';
    case 'js':
    case 'jsx':
    case 'mjs':
    case 'cjs':
      return 'javascript';
    case 'ts':
    case 'tsx':
      return 'typescript';
    case 'yml':
      return 'yaml';
    case 'py':
    case 'pyi':
      return 'python';
    case 'rs':
      return 'rust';
    case 'md':
    case 'markdown':
      return 'markdown';
    case 'toml':
    case 'cfg':
      return 'ini';
    case 'html':
    case 'htm':
    case 'svg':
      return 'xml';
    case 'kt':
    case 'kts':
      return 'kotlin';
    case 'h':
      return 'c';
    case 'cc':
    case 'cxx':
    case 'hpp':
    case 'hh':
      return 'cpp';
    case 'cs':
      return 'csharp';
    case 'rb':
      return 'ruby';
    case 'pl':
    case 'pm':
      return 'perl';
    case 'ex':
    case 'exs':
      return 'elixir';
    case 'erl':
      return 'erlang';
    case 'clj':
      return 'clojure';
    case 'hs':
      return 'haskell';
    case 'ml':
      return 'ocaml';
    case 'proto':
      return 'protobuf';
    case 'gql':
      return 'graphql';
    default:
      return n;
  }
}

/// Highlight grammar for a file [path], keyed on its extension (or the bare
/// filename for extensionless files like `Dockerfile`). Null when unresolved.
String? langFromPath(String? path) {
  final p = path?.trim();
  if (p == null || p.isEmpty) return null;
  final base = p.split('/').last.split('\\').last;
  final dot = base.lastIndexOf('.');
  final key = dot > 0 ? base.substring(dot + 1) : base;
  return _alias(key);
}

/// Returns [body] re-indented as JSON when it parses as a JSON object/array,
/// else null. Bare scalars are left as-is so ordinary output isn't mistaken for
/// JSON.
String? _prettyJson(String body) {
  final t = body.trim();
  if (t.isEmpty) return null;
  final first = t.codeUnitAt(0);
  if (first != 0x7B /* { */ && first != 0x5B /* [ */) return null;
  try {
    return const JsonEncoder.withIndent('  ').convert(jsonDecode(t));
  } catch (_) {
    return null;
  }
}
