import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/entry.dart';
import 'package:argus/ui/item_row.dart';

Widget _wrap(Entry item) =>
    MaterialApp(home: Scaffold(body: ItemRow(item: item)));

void main() {
  testWidgets('tool row shows name and input preview', (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i', kind: EntryKind.tool, toolName: 'Bash', inputPreview: 'ls -la')));
    expect(find.text('Bash'), findsOneWidget);
    expect(find.textContaining('ls -la'), findsOneWidget);
  });

  testWidgets('skill row shows Skill label and identifier', (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.skill,
        toolName: 'Skill',
        inputPreview: 'superpowers:systematic-debugging')));
    expect(find.text('Skill'), findsOneWidget);
    expect(find.textContaining('superpowers:systematic-debugging'), findsOneWidget);
  });

  testWidgets('subagent row shows type and desc', (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.subagent,
        subagents: [Subagent(type: 'Explore', desc: 'find callers')])));
    expect(find.textContaining('Explore'), findsOneWidget);
    expect(find.textContaining('find callers'), findsOneWidget);
  });

  testWidgets('wait_agent row shows the op and target names', (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.subagent,
        toolName: 'wait_agent',
        subagents: [Subagent(id: 'a1', name: 'Volta'), Subagent(id: 'a2')])));
    expect(find.text('Wait Agent'), findsOneWidget);
    expect(find.textContaining('Volta, a2'), findsOneWidget);
  });

  testWidgets('text item renders nothing as a row', (tester) async {
    await tester.pumpWidget(
        _wrap(const Entry(id: 'i', kind: EntryKind.text, text: 'hello')));
    expect(find.text('hello'), findsNothing);
  });
}
