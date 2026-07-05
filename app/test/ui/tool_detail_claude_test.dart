import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/ui/item_row.dart';
import 'package:argus/ui/tool_detail.dart';

Widget _wrapDetail(Item i) => MaterialApp(
    home: Scaffold(body: SingleChildScrollView(child: toolDetailBody(i))));

Widget _wrapRow(Item i) =>
    MaterialApp(home: Scaffold(body: ItemRow(item: i)));

void main() {
  testWidgets('TaskCreate shows subject, active form and description',
      (tester) async {
    await tester.pumpWidget(_wrapDetail(const Item(
      id: 'i',
      kind: ItemKind.tool,
      toolName: 'TaskCreate',
      toolInput:
          '{"subject":"Make reader seekable","description":"Add Seek method","activeForm":"Implementing seeking"}',
    )));
    expect(find.text('Make reader seekable'), findsOneWidget);
    expect(find.text('Implementing seeking'), findsOneWidget);
    expect(find.text('Add Seek method'), findsOneWidget);
  });

  testWidgets('TaskUpdate shows task id and changed fields', (tester) async {
    await tester.pumpWidget(_wrapDetail(const Item(
      id: 'i',
      kind: ItemKind.tool,
      toolName: 'TaskUpdate',
      toolInput: '{"taskId":"5","status":"in_progress"}',
    )));
    expect(find.text('Task 5'), findsOneWidget);
    expect(find.textContaining('status', findRichText: true), findsWidgets);
    expect(find.textContaining('in_progress', findRichText: true), findsWidgets);
  });

  testWidgets('registry drives the row label and checklist icon for TaskCreate',
      (tester) async {
    await tester.pumpWidget(_wrapRow(const Item(
      id: 'i',
      kind: ItemKind.tool,
      toolName: 'TaskCreate',
      inputPreview: 'Make reader seekable',
    )));
    expect(find.text('Task Create'), findsOneWidget);
    expect(find.byIcon(Icons.checklist), findsOneWidget);
  });

  testWidgets('built-in Bash still renders via the switch', (tester) async {
    await tester.pumpWidget(_wrapDetail(const Item(
      id: 'i',
      kind: ItemKind.tool,
      toolName: 'Bash',
      toolInput: '{"command":"ls -la","description":"list"}',
      result: 'total 0',
    )));
    expect(find.textContaining('ls -la'), findsOneWidget);
    expect(find.textContaining('total 0'), findsOneWidget);
  });
}
