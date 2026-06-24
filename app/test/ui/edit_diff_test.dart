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
}
