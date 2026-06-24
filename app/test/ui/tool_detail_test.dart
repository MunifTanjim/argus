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
