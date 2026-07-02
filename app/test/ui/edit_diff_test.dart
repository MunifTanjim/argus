import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/ui/edit_diff.dart';

Widget _wrap(Item i) => MaterialApp(home: Scaffold(body: editDiffView(i)));

void main() {
  testWidgets('Edit shows path, old and new', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'Edit',
        toolInput:
            '{"file_path":"/a/b.dart","old_string":"foo()","new_string":"bar()"}')));
    expect(find.textContaining('/a/b.dart'), findsOneWidget);
    expect(find.textContaining('foo()'), findsOneWidget);
    expect(find.textContaining('bar()'), findsOneWidget);
  });

  testWidgets('Write shows only content', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'Write',
        toolInput: '{"file_path":"/x.txt","content":"new file body"}')));
    expect(find.textContaining('new file body'), findsOneWidget);
  });

  testWidgets('MultiEdit shows each edit', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'MultiEdit',
        toolInput:
            '{"file_path":"/m.dart","edits":[{"old_string":"a1","new_string":"b1"},{"old_string":"a2","new_string":"b2"}]}')));
    expect(find.textContaining('a1'), findsOneWidget);
    expect(find.textContaining('b2'), findsOneWidget);
  });

  testWidgets('Edit interleaves changed lines and keeps context', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'Edit',
        toolInput:
            '{"file_path":"/a.dart","old_string":"keep\\nbefore\\ntail","new_string":"keep\\nafter\\ntail"}')));
    // Changed lines are marked +/-, the shared lines stay as plain context (so
    // "keep"/"tail" are never shown as removed).
    expect(find.textContaining('- before'), findsOneWidget);
    expect(find.textContaining('+ after'), findsOneWidget);
    expect(find.textContaining('- keep'), findsNothing);
    expect(find.textContaining('- tail'), findsNothing);
  });

  testWidgets('diff header wrap toggle switches horizontal scrolling',
      (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'Edit',
        toolInput:
            '{"file_path":"/a.dart","old_string":"foo()","new_string":"bar()"}')));
    expect(find.text('diff'), findsOneWidget);
    expect(find.byType(SingleChildScrollView), findsOneWidget);
    await tester.tap(find.byIcon(Icons.wrap_text));
    await tester.pump();
    expect(find.byType(SingleChildScrollView), findsNothing);
  });

  testWidgets('diff has a line-number toggle numbering the new side',
      (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'Edit',
        toolInput:
            '{"file_path":"/a.dart","old_string":"keep\\nbefore\\ntail","new_string":"keep\\nafter\\ntail"}')));
    expect(find.text('1'), findsNothing); // off by default
    await tester.tap(find.byIcon(Icons.format_list_numbered));
    await tester.pump();
    // New side: keep(1), after(2), tail(3). The deleted "before" has no number.
    expect(find.text('1'), findsOneWidget);
    expect(find.text('2'), findsOneWidget);
    expect(find.text('3'), findsOneWidget);
  });
}
