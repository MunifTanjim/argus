import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:gpt_markdown/gpt_markdown.dart';
import 'package:re_highlight/languages/all.dart';
import 'package:re_highlight/re_highlight.dart';
import 'package:re_highlight/styles/all.dart';

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

/// Renders markdown with app-consistent code blocks: fenced code goes through the
/// same [codeView] renderer standalone blocks use, so it gets syntax highlighting
/// and is copyable.
Widget appMarkdown(String data) => GptMarkdown(
      data,
      codeBuilder: (context, name, code, closed) => codeView(code, lang: name),
    );

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
/// wrap state (still user-toggleable).
Widget codeView(String body,
    {String? lang, bool lineNumberToggle = true, bool wrap = false}) {
  // Show the box with a dim marker so an empty block still reads as "a block that
  // was empty" rather than a missing render.
  if (body.trim().isEmpty) {
    return _container(
        Text('(empty)', style: _codeMono.copyWith(color: AppColors.dim)));
  }
  return _CodeBlock(
      body: body, lang: lang, lineNumberToggle: lineNumberToggle, wrap: wrap);
}

class _CodeBlock extends StatefulWidget {
  const _CodeBlock(
      {required this.body,
      this.lang,
      this.lineNumberToggle = true,
      this.wrap = false});

  final String body;
  final String? lang;
  final bool lineNumberToggle;
  final bool wrap;

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
            child: _wrap
                ? content
                : SingleChildScrollView(
                    scrollDirection: Axis.horizontal, child: content),
          ),
        ],
      ),
    );
  }

  Widget _numbered(List<List<_Run>> lines) {
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

  Widget _lineText(List<_Run> runs) => Text.rich(
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
class _Run {
  const _Run(this.text, this.style);
  final String text;
  final TextStyle? style;
}

/// Flattens a highlighted [span] tree into per-line runs so a line-number gutter
/// can align to each logical line. Text renders before children within a span,
/// so the walk order matches on-screen order.
List<List<_Run>> _spanLines(InlineSpan span) {
  final lines = <List<_Run>>[<_Run>[]];
  void walk(InlineSpan s, TextStyle? inherited) {
    if (s is! TextSpan) return;
    final style = inherited == null ? s.style : inherited.merge(s.style);
    final text = s.text;
    if (text != null) {
      final parts = text.split('\n');
      for (var i = 0; i < parts.length; i++) {
        if (i > 0) lines.add(<_Run>[]);
        if (parts[i].isNotEmpty) lines.last.add(_Run(parts[i], style));
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

/// Copies [text] to the clipboard and shows a brief confirmation snackbar when a
/// messenger is available.
void copyToClipboard(BuildContext context, String text) {
  Clipboard.setData(ClipboardData(text: text));
  ScaffoldMessenger.maybeOf(context)?.showSnackBar(const SnackBar(
    content: Text('Copied'),
    duration: Duration(seconds: 1),
  ));
}

Widget _container(Widget child) => Container(
      width: double.infinity,
      margin: const EdgeInsets.symmetric(vertical: 4),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: AppColors.canvas,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: AppColors.border),
      ),
      child:
          SingleChildScrollView(scrollDirection: Axis.horizontal, child: child),
    );

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
      return 'javascript';
    case 'ts':
      return 'typescript';
    case 'yml':
      return 'yaml';
    case 'py':
      return 'python';
    case 'rs':
      return 'rust';
    case 'md':
      return 'markdown';
    default:
      return n;
  }
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
