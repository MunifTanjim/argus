import 'dart:convert';

import 'package:flutter/material.dart';

import '../models/entry.dart';
import 'code_block.dart';
import 'theme.dart';

/// Helpers shared by the per-agent tool detail renderers.

/// The entry's tool input as a JSON object; empty when it is not one.
Map<String, dynamic> toolInput(Entry it) {
  try {
    return jsonDecode(it.toolInput ?? '') as Map<String, dynamic>;
  } catch (_) {
    return const {};
  }
}

/// A section label, such as "Result", in the error color when [error].
Widget toolLabel(String text, ArgusTokens t, {bool error = false}) => Padding(
      padding: const EdgeInsets.only(top: 8, bottom: 4),
      child: Text(text,
          style: TextStyle(
              color: error ? t.error : t.textSecondary,
              fontWeight: FontWeight.w700,
              fontSize: 13)),
    );

/// The tool result under its "Result" label, or "Error" in the error color;
/// nothing when [body] is null.
Widget toolResult(Entry it, Widget? body, ArgusTokens t) => body == null
    ? const SizedBox.shrink()
    : Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      toolLabel(it.resultIsError ? 'Error' : 'Result', t,
          error: it.resultIsError),
      body,
    ]);

/// The raw tool input and result as code, for a tool with no renderer.
/// [resultText] cleans the result text before it is shown.
Widget toolGeneric(Entry it, ArgusTokens t,
        {String? resultLang, String Function(String)? resultText}) =>
    Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      if ((it.toolInput ?? '').isNotEmpty) ...[
        toolLabel('Input', t),
        codeBlock(it.toolInput!),
      ],
      if ((it.result ?? '').isNotEmpty)
        toolResult(
            it,
            codeBlock(resultText?.call(it.result!) ?? it.result!,
                lang: resultLang),
            t),
    ]);

/// The bold mono style of a tool's header line.
TextStyle toolHeaderStyle(ArgusTokens t) =>
    codeMono.copyWith(color: t.textSecondary, fontWeight: FontWeight.w700);

/// A bold mono line, such as a command or a path.
Widget toolHeader(String text, ArgusTokens t) =>
    Text(text, style: toolHeaderStyle(t));

/// A bold [lead], then [rest], then an optional dim `(dim)`.
Widget toolHeadline(String lead, String rest, ArgusTokens t, [String? dim]) =>
    RichText(
      text: TextSpan(style: codeMono, children: [
        TextSpan(text: lead, style: toolHeaderStyle(t)),
        TextSpan(text: rest, style: codeMono.copyWith(color: t.text)),
        if (dim != null)
          TextSpan(text: '  ($dim)', style: codeMono.copyWith(color: t.textDim)),
      ]),
    );

/// A dim mono `# text` line.
Widget toolComment(String text, ArgusTokens t) =>
    Text('# $text', style: codeMono.copyWith(color: t.textDim));

/// `key: value` lines with the key dim; other lines as they are.
Widget kvDump(String s, ArgusTokens t) {
  final rows = <Widget>[];
  for (final raw in s.split('\n')) {
    final line = raw.trimRight();
    if (line.trim().isEmpty) continue;
    final idx = line.indexOf(':');
    if (idx > 0) {
      rows.add(RichText(
        text: TextSpan(style: codeMono, children: [
          TextSpan(
              text: line.substring(0, idx + 1),
              style: codeMono.copyWith(color: t.textDim)),
          TextSpan(
              text: line.substring(idx + 1),
              style: codeMono.copyWith(color: t.text)),
        ]),
      ));
    } else {
      rows.add(Text(line, style: codeMono.copyWith(color: t.text)));
    }
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: rows);
}
