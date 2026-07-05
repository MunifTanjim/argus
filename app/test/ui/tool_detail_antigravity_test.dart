import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/ui/tool_detail.dart';
import 'package:argus/ui/tool_detail_antigravity.dart';

Widget _wrap(Item i) => MaterialApp(
    home: Scaffold(body: SingleChildScrollView(child: toolDetailBody(i))));

void main() {
  group('result helpers', () {
    test('agyResultBody strips leading timestamp lines', () {
      const r = 'Created At: x\nCompleted At: y\nreal payload\nmore';
      expect(agyResultBody(r), 'real payload\nmore');
    });

    test('agyResultBody leaves untimestamped bodies untouched', () {
      expect(agyResultBody('just text'), 'just text');
    });

    test('stripAgyReminder cuts at REMINDER:', () {
      expect(stripAgyReminder('status: ok\nREMINDER: do not poll'), 'status: ok');
    });

    test('stripAgyBoilerplate removes the appended instruction', () {
      const r =
          "Created file.\nIf relevant, proactively run terminal commands to execute this code for the USER. Don't ask for permission.";
      expect(stripAgyBoilerplate(r), 'Created file.');
    });

    test('formatBytes scales units', () {
      expect(formatBytes(512), '512B');
      expect(formatBytes(1536), '1.5k');
      expect(formatBytes(1572864), '1.5M');
    });

    test('splitRunCommandResult splits at the tab-indented Output marker', () {
      const r = 'Created At: x\n\tExit Code: 0\n\tOutput:\n\thello\n\tworld';
      final body = agyResultBody(r);
      final s = splitRunCommandResult(body);
      expect(s.hasMarker, isTrue);
      expect(s.head.contains('Exit Code: 0'), isTrue);
      expect(s.output, 'hello\nworld');
    });

    test('splitRunCommandResult with no marker keeps whole body', () {
      final s = splitRunCommandResult('Task started in background.');
      expect(s.hasMarker, isFalse);
      expect(s.output, '');
    });

    test('grepRows formats JSON matches as file:line content', () {
      const body =
          '{"File":"a.dart","LineNumber":12,"LineContent":"  final x = 1;"}';
      expect(grepRows(body), ['a.dart:12 final x = 1;']);
    });

    test('listDirRows marks dirs and sizes files', () {
      const body =
          '{"name":"src","isDir":true}\n{"name":"main.dart","isDir":false,"sizeBytes":"2048"}';
      expect(listDirRows(body), ['src/', 'main.dart  2.0k']);
    });

    test('splitViewFileResult separates meta from numbered content', () {
      const r =
          'Created At: x\nFile Path: `a.go`\nTotal Lines: 2\nTotal Bytes: 20\nShowing lines 1 to 2\nThe following code has line numbers.\n1: package main\n2: func main() {}';
      final v = splitViewFileResult(r);
      expect(v.meta.contains('Total Lines: 2'), isTrue);
      expect(v.content, '1: package main\n2: func main() {}');
    });

    test('parseAgyAnswers maps A1/A2 to 0-based indices', () {
      expect(parseAgyAnswers('A1: yes\nA2: no'), {0: 'yes', 1: 'no'});
    });

    test('agyImageResult drops boilerplate and the Using prompt echo', () {
      const r =
          'Created At: x\nUsing prompt: a cat\nSaved to /tmp/cat.png\nDo not output the path of this image to show to the user since the user can already see it. However, you can embed this image in artifacts for the USER\'s review.';
      expect(agyImageResult(r), 'Saved to /tmp/cat.png');
    });
  });

  group('renderers', () {
    testWidgets('run_command shows cwd, command and split output',
        (tester) async {
      await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'run_command',
        toolInput: '{"CommandLine":"go test ./...","Cwd":"/repo"}',
        result: 'Created At: x\n\tExit Code: 0\n\tOutput:\n\tok',
      )));
      expect(find.textContaining('go test ./...'), findsOneWidget);
      expect(find.textContaining('/repo'), findsOneWidget);
      expect(find.textContaining('ok'), findsWidgets);
    });

    testWidgets('write_to_file renders an all-additions diff', (tester) async {
      await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'write_to_file',
        toolInput:
            '{"TargetFile":"a.txt","CodeContent":"line one\\nline two"}',
      )));
      expect(find.text('diff'), findsOneWidget); // diff box header
      expect(find.textContaining('a.txt'), findsOneWidget);
    });

    testWidgets('ask_question marks the chosen option', (tester) async {
      await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'ask_question',
        toolInput:
            '{"questions":[{"question":"Pick one","is_multi_select":false,"options":["Alpha","Beta"]}]}',
        result: 'A1: Beta',
      )));
      expect(find.textContaining('◉ Beta'), findsOneWidget);
      expect(find.textContaining('○ Alpha'), findsOneWidget);
    });

    testWidgets('grep_search renders header and matches', (tester) async {
      await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'grep_search',
        toolInput: '{"Query":"foo","SearchPath":"lib","IsRegex":true}',
        result:
            'Created At: x\n{"File":"lib/a.dart","LineNumber":3,"LineContent":"foo()"}',
      )));
      expect(find.textContaining('"foo"', findRichText: true), findsWidgets);
      expect(find.textContaining('lib/a.dart:3 foo()', findRichText: true),
          findsWidgets);
    });
  });
}
