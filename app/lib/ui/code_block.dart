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
Widget codeBlock(String body, {String? lang}) => codeView(body, lang: lang);

/// Renders [body] as a syntax-highlighted code block via re_highlight
/// (highlight.js grammars). JSON is pretty-printed first. Long-press to copy the
/// raw text.
Widget codeView(String body, {String? lang}) {
  if (body.trim().isEmpty) return const SizedBox.shrink();
  final pretty = _prettyJson(body);
  final source = pretty ?? body;
  // An unknown fence language (or any highlighter hiccup) must not break the
  // block — fall back to plain monospace.
  TextSpan span;
  try {
    final result = _highlight.highlightAuto(
        source, _candidates(pretty != null ? 'json' : lang));
    final renderer = TextSpanRenderer(_codeMono, _codeTheme);
    result.render(renderer);
    span = renderer.span ?? TextSpan(text: source, style: _codeMono);
  } catch (_) {
    span = TextSpan(text: source, style: _codeMono);
  }
  return CopyOnLongPress(text: body, child: _container(Text.rich(span)));
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

/// Wraps [child] so a long-press copies [text] to the clipboard, with a brief
/// confirmation snackbar when a messenger is available.
class CopyOnLongPress extends StatelessWidget {
  const CopyOnLongPress({super.key, required this.text, required this.child});

  final String text;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onLongPress: () {
        Clipboard.setData(ClipboardData(text: text));
        ScaffoldMessenger.maybeOf(context)?.showSnackBar(const SnackBar(
          content: Text('Copied'),
          duration: Duration(seconds: 1),
        ));
      },
      child: child,
    );
  }
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
