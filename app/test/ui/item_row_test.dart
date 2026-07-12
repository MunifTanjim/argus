import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/ui/item_row.dart';

Widget _wrap(Item item) =>
    MaterialApp(home: Scaffold(body: ItemRow(item: item)));

void main() {
  testWidgets('tool row shows name and input preview', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i', kind: ItemKind.tool, toolName: 'Bash', inputPreview: 'ls -la')));
    expect(find.text('Bash'), findsOneWidget);
    expect(find.textContaining('ls -la'), findsOneWidget);
  });

  testWidgets('skill row shows Skill label and identifier', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.skill,
        toolName: 'Skill',
        inputPreview: 'superpowers:systematic-debugging')));
    expect(find.text('Skill'), findsOneWidget);
    expect(find.textContaining('superpowers:systematic-debugging'), findsOneWidget);
  });

  testWidgets('subagent row shows type and desc', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.subagent,
        subagents: [Subagent(type: 'Explore', desc: 'find callers')])));
    expect(find.textContaining('Explore'), findsOneWidget);
    expect(find.textContaining('find callers'), findsOneWidget);
  });

  testWidgets('wait_agent row shows the op and target names', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.subagent,
        toolName: 'wait_agent',
        subagents: [Subagent(id: 'a1', name: 'Volta'), Subagent(id: 'a2')])));
    expect(find.text('Wait Agent'), findsOneWidget);
    expect(find.textContaining('Volta, a2'), findsOneWidget);
  });

  testWidgets('thinking row shows label', (tester) async {
    await tester.pumpWidget(_wrap(
        const Item(id: 'i', kind: ItemKind.thinking, text: 'pondering')));
    expect(find.textContaining('Thinking'), findsOneWidget);
  });

  testWidgets('text item renders nothing as a row', (tester) async {
    await tester.pumpWidget(
        _wrap(const Item(id: 'i', kind: ItemKind.text, text: 'hello')));
    expect(find.text('hello'), findsNothing);
  });
}
