import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/ui/edit_diff.dart';

Widget _wrap(Item i) => MaterialApp(home: Scaffold(body: editDiffView(i)));

Widget _wrapDiff(String oldS, String newS) => MaterialApp(
    home: Scaffold(
        body: SingleChildScrollView(child: diffView(oldS, newS))));

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

  // The faint per-row tints marking add/removed lines.
  final greenTint = const Color(0xFFb8bb26).withValues(alpha: 0.10);
  final redTint = const Color(0xFFfb4934).withValues(alpha: 0.10);
  Finder tinted(Color c) =>
      find.byWidgetPredicate((w) => w is ColoredBox && w.color == c);

  testWidgets('highlighted rows carry a green/red tint by add/del',
      (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'Edit',
        toolInput:
            '{"file_path":"/a.dart","old_string":"x","new_string":"y"}')));
    // Line-number view lays each row out with a full-width tinted background.
    await tester.tap(find.byIcon(Icons.format_list_numbered));
    await tester.pump();
    expect(tinted(redTint), findsOneWidget); // "- x"
    expect(tinted(greenTint), findsOneWidget); // "+ y"
  });

  testWidgets('disabling highlight drops the row tints', (tester) async {
    await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'Edit',
        toolInput:
            '{"file_path":"/a.dart","old_string":"x","new_string":"y"}')));
    await tester.tap(find.byIcon(Icons.format_list_numbered));
    await tester.pump();
    expect(tinted(redTint), findsOneWidget);
    await tester.tap(find.byIcon(Icons.format_color_reset));
    await tester.pump();
    expect(tinted(redTint), findsNothing);
    expect(tinted(greenTint), findsNothing);
  });

  group('trailing newline and CRLF fidelity', () {
    testWidgets('removing the final newline is shown, not swallowed',
        (tester) async {
      await tester.pumpWidget(_wrapDiff('foo\n', 'foo'));
      // The change is now visible (last line re-diffed) with git's marker.
      expect(find.textContaining('No newline at end of file'), findsOneWidget);
      expect(find.textContaining('- foo'), findsOneWidget);
      expect(find.textContaining('+ foo'), findsOneWidget);
    });

    testWidgets('adding a final newline is shown', (tester) async {
      await tester.pumpWidget(_wrapDiff('foo', 'foo\n'));
      expect(find.textContaining('No newline at end of file'), findsOneWidget);
    });

    testWidgets('a trailing-newline change touches only the last line',
        (tester) async {
      await tester.pumpWidget(_wrapDiff('a\nb', 'a\nb\n'));
      // "a" stays shared context; only "b" is re-diffed for its newline.
      expect(find.textContaining('- b'), findsOneWidget);
      expect(find.textContaining('+ b'), findsOneWidget);
      expect(find.textContaining('- a'), findsNothing);
    });

    testWidgets('CRLF vs LF with identical content shows no changes',
        (tester) async {
      await tester.pumpWidget(_wrapDiff('a\nb\n', 'a\r\nb\r\n'));
      expect(find.textContaining('- '), findsNothing);
      expect(find.textContaining('+ '), findsNothing);
      expect(find.textContaining('No newline at end of file'), findsNothing);
    });
  });
}
