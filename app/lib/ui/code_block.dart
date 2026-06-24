import 'package:flutter/material.dart';
import 'package:gpt_markdown/gpt_markdown.dart';

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

/// Renders [body] as a fenced code block through gpt_markdown.
Widget codeBlock(String body, {String? lang}) {
  if (body.trim().isEmpty) return const SizedBox.shrink();
  final fence = safeFence(body);
  return GptMarkdown('$fence${lang ?? ''}\n$body\n$fence');
}
