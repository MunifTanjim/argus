import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/ui/tool_detail.dart';

Widget _wrap(Item i) => MaterialApp(
    home: Scaffold(body: SingleChildScrollView(child: toolDetailBody(i))));

void main() {
  testWidgets('Bash shows command and result', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'Bash',
        toolInput: '{"command":"ls -la","description":"list"}',
        result: 'total 0')));
    expect(find.textContaining('ls -la'), findsOneWidget);
    expect(find.textContaining('total 0'), findsOneWidget);
  });

  testWidgets('Bash command renders as a copyable code block', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'Bash',
        toolInput: '{"command":"echo one\\necho two"}')));
    expect(find.text('bash'), findsOneWidget); // code block header label
    await tester.tap(find.byIcon(Icons.copy));
    await tester.pump();
    expect(find.text('Copied'), findsOneWidget);
  });

  testWidgets('EnterPlanMode result is labelled markdown', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'EnterPlanMode',
        toolInput: '{}',
        result: '## Plan\n\n- step one')));
    expect(find.text('markdown'), findsOneWidget); // code block header label
  });

  testWidgets('ExitPlanMode shows plan file, plan and result as markdown',
      (tester) async {
    await tester.pumpWidget(_wrap(Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'ExitPlanMode',
        toolInput: jsonEncode({
          'plan': '## Heading\n\n- step one',
          'planFilePath': '/home/u/.claude/plans/x.md',
          'allowedPrompts': [
            {'tool': 'Bash', 'prompt': 'y'}
          ],
        }),
        result: '## Approved\n\nGo ahead now')));
    // Plan file path rendered pretty, not as raw JSON.
    expect(find.text('Plan file'), findsOneWidget);
    expect(find.textContaining('/home/u/.claude/plans/x.md'), findsOneWidget);
    // Plan and result render as markdown, not highlighted code blocks.
    expect(find.text('markdown'), findsNothing);
    expect(find.text('json'), findsNothing);
    expect(find.textContaining('step one'), findsOneWidget);
    expect(find.textContaining('Go ahead now'), findsOneWidget);
  });

  testWidgets('ExitPlanMode collapses a long plan behind a toggle',
      (tester) async {
    final longPlan = List.generate(30, (i) => '- item $i').join('\n');
    await tester.pumpWidget(_wrap(Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'ExitPlanMode',
        toolInput: jsonEncode({'plan': longPlan, 'planFilePath': '/p.md'}))));

    expect(find.text('Show more'), findsOneWidget);
    expect(find.text('Show less'), findsNothing);
    await tester.tap(find.byKey(const Key('plan-toggle')));
    await tester.pump();
    expect(find.text('Show less'), findsOneWidget);
  });

  for (final tool in const ['WebFetch', 'WebSearch']) {
    testWidgets('$tool result renders as markdown', (tester) async {
      await tester.pumpWidget(_wrap(Item(
          id: 'i',
          kind: ItemKind.tool,
          toolName: tool,
          toolInput:
              tool == 'WebFetch' ? '{"url":"https://x.dev"}' : '{"query":"q"}',
          result: '# Heading\n\nbody text')));
      expect(find.text('markdown'), findsNothing); // no code-block header
      expect(find.textContaining('Heading'), findsOneWidget);
      expect(find.textContaining('body text'), findsOneWidget);
      // Input and output are clearly labelled.
      expect(find.text(tool == 'WebFetch' ? 'URL' : 'Query'), findsOneWidget);
      expect(find.text('Result'), findsOneWidget);
    });
  }

  testWidgets('Read infers language from the file extension', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'Read',
        toolInput: '{"file_path":"/x/foo.py"}',
        result: '     1\tprint("hi")')));
    expect(find.text('python'), findsOneWidget); // .py → python, not auto-detect
  });

  testWidgets('Read result hides the line-number toggle', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'Read',
        toolInput: '{"file_path":"/a.dart"}',
        result: '     1\tline one\n     2\tline two')));
    expect(find.byIcon(Icons.format_list_numbered), findsNothing);
  });

  testWidgets('Grep shows pattern header', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'Grep',
        toolInput: '{"pattern":"foo","path":"lib"}',
        result: 'lib/a.dart:1')));
    expect(find.textContaining('foo'), findsOneWidget);
    expect(find.textContaining('lib'), findsWidgets);
  });

  testWidgets('TodoWrite renders checklist', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'TodoWrite',
        toolInput:
            '{"todos":[{"content":"do a","status":"completed","activeForm":"doing a"},{"content":"do b","status":"in_progress","activeForm":"doing b"}]}')));
    expect(find.textContaining('do a'), findsOneWidget);
    expect(find.textContaining('doing b'), findsOneWidget);
  });

  testWidgets('generic fallback shows input and result', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'MysteryTool',
        toolInput: '{"x":1}',
        result: 'done')));
    expect(find.textContaining('done'), findsOneWidget);
  });

  test('answeredAnswer parses question→answer pairs', () {
    const r =
        'Your questions have been answered: "Pick one"="A", "Colors"="red, blue"';
    expect(answeredAnswer(r, 'Pick one'), 'A');
    expect(answeredAnswer(r, 'Colors'), 'red, blue');
    expect(answeredAnswer(r, 'Absent'), isNull);
  });
}
