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

  for (final tool in const ['ExitPlanMode', 'EnterPlanMode']) {
    testWidgets('$tool result is labelled markdown', (tester) async {
      await tester.pumpWidget(_wrap(Item(
          id: 'i',
          kind: ItemKind.tool,
          toolName: tool,
          toolInput: '{"plan":"do things"}',
          result: '## Plan\n\n- step one')));
      expect(find.text('markdown'), findsOneWidget);
    });
  }

  testWidgets('Web result is labelled markdown', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'WebFetch',
        toolInput: '{"url":"https://x.dev"}',
        result: '# Heading\n\nbody text')));
    expect(find.text('markdown'), findsOneWidget);
  });

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
