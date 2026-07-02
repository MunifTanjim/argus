import 'dart:convert';

import 'package:flutter/material.dart';

import '../models/chunk.dart';
import 'code_block.dart';
import 'edit_diff.dart';
import 'theme.dart';

const _red = Color(0xFFfb4934);
const _mono = TextStyle(fontFamily: 'monospace', fontSize: 12, height: 1.35);

Map<String, dynamic> _input(Item it) {
  try {
    return jsonDecode(it.toolInput ?? '') as Map<String, dynamic>;
  } catch (_) {
    return const {};
  }
}

Widget _label(String text, {bool error = false}) => Padding(
      padding: const EdgeInsets.only(top: 8, bottom: 4),
      child: Text(text,
          style: TextStyle(
              color: error ? _red : AppColors.secondary,
              fontWeight: FontWeight.w700,
              fontSize: 13)),
    );

Widget _header(String text) => Text(text,
    style:
        _mono.copyWith(color: AppColors.secondary, fontWeight: FontWeight.w700));

Widget _resultSection(Item it, {bool wrap = false, String? lang}) {
  if ((it.result ?? '').isEmpty) return const SizedBox.shrink();
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    _label(it.resultIsError ? 'Error' : 'Result', error: it.resultIsError),
    codeBlock(it.result!, wrap: wrap, lang: lang),
  ]);
}

Widget toolDetailBody(Item item) {
  switch (item.toolName) {
    case 'Edit':
    case 'MultiEdit':
    case 'Write':
    case 'NotebookEdit':
      return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        editDiffView(item),
        // Edit results echo the changed file region; wrap so long lines stay
        // readable without horizontal scrolling.
        _resultSection(item, wrap: true),
      ]);
    case 'Bash':
      return _bash(item);
    case 'Read':
    case 'NotebookRead':
      return _read(item);
    case 'Grep':
      return _grep(item);
    case 'Glob':
    case 'LS':
      return _glob(item);
    case 'WebFetch':
    case 'WebSearch':
      return _web(item);
    case 'TodoWrite':
      return _todo(item);
    case 'AskUserQuestion':
      return _askUserQuestion(item);
    case 'EnterPlanMode':
    case 'ExitPlanMode':
      return _generic(item, resultLang: 'markdown');
    default:
      return _generic(item);
  }
}

Widget _generic(Item it, {String? resultLang}) =>
    Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      if ((it.toolInput ?? '').isNotEmpty) ...[
        _label('Input'),
        codeBlock(it.toolInput!),
      ],
      _resultSection(it, lang: resultLang),
    ]);

Widget _bash(Item it) {
  final m = _input(it);
  final desc = toolInputStr(m['description']), cmd = toolInputStr(m['command']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (desc.isNotEmpty)
      Text('# $desc', style: _mono.copyWith(color: AppColors.dim)),
    // Commands are frequently multi-line (heredocs, `&&` chains) — a bash code
    // block gives highlighting, wrap, and copy for free.
    if (cmd.isNotEmpty)
      codeBlock(cmd, lang: 'bash')
    else if ((it.toolInput ?? '').isNotEmpty)
      codeBlock(it.toolInput!),
    _resultSection(it),
  ]);
}

Widget _read(Item it) {
  final path = toolInputStr(_input(it)['file_path']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (path.isNotEmpty) _header(path),
    // Prefer the file extension; fall back to auto-detection when unknown.
    // Read output is already `cat -n` numbered — no need for our own gutter.
    if ((it.result ?? '').isNotEmpty)
      codeBlock(it.result!,
          lang: _langFromPath(path), lineNumberToggle: false),
  ]);
}

/// Maps a file path's extension (or bare name) to a re_highlight grammar, or
/// null when unknown so the highlighter auto-detects instead.
String? _langFromPath(String path) {
  final slash = path.lastIndexOf('/');
  final name = (slash >= 0 ? path.substring(slash + 1) : path).toLowerCase();
  final dot = name.lastIndexOf('.');
  final key = dot > 0 ? name.substring(dot + 1) : name;
  return _extLang[key];
}

const _extLang = <String, String>{
  'dart': 'dart',
  'go': 'go',
  'py': 'python',
  'pyi': 'python',
  'js': 'javascript',
  'mjs': 'javascript',
  'cjs': 'javascript',
  'jsx': 'javascript',
  'ts': 'typescript',
  'tsx': 'typescript',
  'json': 'json',
  'yaml': 'yaml',
  'yml': 'yaml',
  'toml': 'ini',
  'ini': 'ini',
  'cfg': 'ini',
  'sh': 'bash',
  'bash': 'bash',
  'zsh': 'bash',
  'rs': 'rust',
  'md': 'markdown',
  'markdown': 'markdown',
  'html': 'xml',
  'htm': 'xml',
  'xml': 'xml',
  'svg': 'xml',
  'css': 'css',
  'scss': 'scss',
  'less': 'less',
  'java': 'java',
  'kt': 'kotlin',
  'kts': 'kotlin',
  'swift': 'swift',
  'c': 'c',
  'h': 'c',
  'cpp': 'cpp',
  'cc': 'cpp',
  'cxx': 'cpp',
  'hpp': 'cpp',
  'hh': 'cpp',
  'cs': 'csharp',
  'rb': 'ruby',
  'php': 'php',
  'sql': 'sql',
  'lua': 'lua',
  'r': 'r',
  'scala': 'scala',
  'pl': 'perl',
  'pm': 'perl',
  'ex': 'elixir',
  'exs': 'elixir',
  'erl': 'erlang',
  'clj': 'clojure',
  'hs': 'haskell',
  'ml': 'ocaml',
  'proto': 'protobuf',
  'graphql': 'graphql',
  'gql': 'graphql',
  'makefile': 'makefile',
  'dockerfile': 'dockerfile',
};

Widget _grep(Item it) {
  final m = _input(it);
  var scope = toolInputStr(m['glob']);
  final path = toolInputStr(m['path']);
  if (path.isNotEmpty) scope = scope.isEmpty ? path : '$scope $path';
  final head = '"${toolInputStr(m['pattern'])}"${scope.isEmpty ? '' : ' in $scope'}';
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    _header(head),
    if ((it.result ?? '').isNotEmpty) codeBlock(it.result!),
  ]);
}

Widget _glob(Item it) {
  final m = _input(it);
  final head = toolInputStr(m['pattern']).isNotEmpty ? toolInputStr(m['pattern']) : toolInputStr(m['path']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (head.isNotEmpty) _header(head),
    if ((it.result ?? '').isNotEmpty) codeBlock(it.result!),
  ]);
}

Widget _web(Item it) {
  final m = _input(it);
  final head = toolInputStr(m['url']).isNotEmpty ? toolInputStr(m['url']) : toolInputStr(m['query']);
  final prompt = toolInputStr(m['prompt']);
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    if (head.isNotEmpty) _header(head),
    if (prompt.isNotEmpty)
      Text('# $prompt', style: _mono.copyWith(color: AppColors.dim)),
    // Web results come back as markdown.
    if ((it.result ?? '').isNotEmpty) codeBlock(it.result!, lang: 'markdown'),
  ]);
}

Widget _todo(Item it) {
  final todos = (_input(it)['todos'] as List?) ?? const [];
  if (todos.isEmpty) return _generic(it);
  final rows = <Widget>[];
  for (final t in todos.cast<Map<String, dynamic>>()) {
    final status = toolInputStr(t['status']);
    var glyph = '☐', text = toolInputStr(t['content']);
    if (status == 'completed') {
      glyph = '☑';
    } else if (status == 'in_progress') {
      glyph = '◐';
      if (toolInputStr(t['activeForm']).isNotEmpty) text = toolInputStr(t['activeForm']);
    }
    rows.add(Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Text('$glyph $text', style: _mono.copyWith(color: AppColors.text)),
    ));
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: rows);
}

final _answerPair = RegExp(r'"([^"]+)"="([^"]*)"');

String? answeredAnswer(String result, String question) {
  for (final m in _answerPair.allMatches(result)) {
    if (m.group(1) == question) return m.group(2);
  }
  return null;
}

Widget _askUserQuestion(Item it) {
  final qs = (_input(it)['questions'] as List?) ?? const [];
  if (qs.isEmpty) return _generic(it);
  final result = it.result ?? '';
  final blocks = <Widget>[];
  for (final q in qs.cast<Map<String, dynamic>>()) {
    final question = toolInputStr(q['question']);
    final multi = (q['multiSelect'] as bool?) ?? false;
    final chosen = (answeredAnswer(result, question) ?? '')
        .split(', ')
        .map((s) => s.trim())
        .where((s) => s.isNotEmpty)
        .toSet();
    final children = <Widget>[];
    if (toolInputStr(q['header']).isNotEmpty) {
      children.add(Text(toolInputStr(q['header']).toUpperCase(),
          style: _mono.copyWith(
              color: AppColors.accent, fontWeight: FontWeight.w700)));
    }
    if (question.isNotEmpty) children.add(appMarkdown(question));
    for (final opt
        in ((q['options'] as List?) ?? const []).cast<Map<String, dynamic>>()) {
      final label = toolInputStr(opt['label']);
      final isChosen = chosen.remove(label);
      final mark =
          multi ? (isChosen ? '[x]' : '[ ]') : (isChosen ? '◉' : '○');
      children.add(Padding(
        padding: const EdgeInsets.only(top: 6),
        child: Text('$mark $label',
            style: TextStyle(
                color: isChosen ? AppColors.text : AppColors.secondary,
                fontWeight: isChosen ? FontWeight.w700 : FontWeight.w400)),
      ));
      if (toolInputStr(opt['description']).isNotEmpty) {
        children.add(Padding(
          padding: const EdgeInsets.only(left: 20, top: 2),
          child: Text(toolInputStr(opt['description']),
              style: const TextStyle(color: AppColors.dim, fontSize: 13)),
        ));
      }
      if (toolInputStr(opt['preview']).isNotEmpty) {
        children.add(Padding(
          padding: const EdgeInsets.only(left: 20, top: 2),
          child: codeBlock(toolInputStr(opt['preview'])),
        ));
      }
    }
    for (final custom in chosen) {
      children.add(Padding(
        padding: const EdgeInsets.only(top: 6),
        child: Text('Answer: $custom',
            style: const TextStyle(
                color: AppColors.secondary, fontWeight: FontWeight.w700)),
      ));
    }
    blocks.add(Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Column(
          crossAxisAlignment: CrossAxisAlignment.start, children: children),
    ));
  }
  return Column(crossAxisAlignment: CrossAxisAlignment.start, children: blocks);
}
