import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/ui/edit_diff.dart';

// One full-context hunk from prefixed lines (' ' context, '-' old, '+' new).
String _ud(List<String> lines) {
  final oldN = lines.where((l) => !l.startsWith('+')).length;
  final newN = lines.where((l) => !l.startsWith('-')).length;
  return '@@ -1,$oldN +1,$newN @@\n${lines.join('\n')}\n';
}

List<String> _ctx(String prefix, int n) =>
    [for (var i = 0; i < n; i++) ' $prefix$i'];

// The diff scrolls its own content and pins its header, so it needs a
// bounded-height parent (the Scaffold body) rather than an outer scroll view.
Widget _wrap(String diff) => MaterialApp(
      home: Scaffold(body: unifiedDiffView(diff)),
    );

// Mirrors the review screen: a static line above a Flexible diff, so a short
// diff stays compact and a long one caps at the viewport.
Widget _screen(String diff) => MaterialApp(
      home: Scaffold(
        body: Column(
          children: [
            const Text('header'),
            Flexible(child: unifiedDiffView(diff)),
          ],
        ),
      ),
    );

// Height of the outer vertical scroller wrapping the diff rows.
double _scrollerHeight(WidgetTester tester) =>
    tester.getSize(find.byType(SingleChildScrollView).first).height;

void main() {
  testWidgets('folds far context, shows the change, expands on demand',
      (tester) async {
    await tester.pumpWidget(_wrap(_ud([..._ctx('l', 20), '-old', '+new'])));

    expect(find.textContaining('- old'), findsOneWidget);
    expect(find.textContaining('+ new'), findsOneWidget);
    expect(find.text('  l19'), findsOneWidget); // within 3 of the change
    expect(find.textContaining('Expand'), findsOneWidget);
    expect(find.text('  l10'), findsNothing);

    await tester.tap(find.byIcon(Icons.unfold_more));
    await tester.pump();
    expect(find.text('  l10'), findsOneWidget);
    expect(find.textContaining('Expand'), findsNothing);
  });

  testWidgets('tapping an expand bar reveals the whole folded run',
      (tester) async {
    await tester.pumpWidget(_wrap(_ud([..._ctx('l', 20), '-old', '+new'])));

    // The hunk row keeps l0..l2 and the change keeps l17..l19.
    expect(find.textContaining('Expand 14 unchanged lines'), findsOneWidget);

    await tester.tap(find.textContaining('Expand'));
    await tester.pump();

    expect(find.text('  l3'), findsOneWidget);
    expect(find.text('  l16'), findsOneWidget);
    expect(find.textContaining('Expand'), findsNothing);
  });

  testWidgets('a short diff stays compact instead of filling the viewport',
      (tester) async {
    await tester.pumpWidget(_screen(_ud(['-a', '+b'])));
    expect(_scrollerHeight(tester), lessThan(200));
  });

  testWidgets('a tall diff caps at the viewport and scrolls', (tester) async {
    await tester.pumpWidget(
        _screen(_ud([for (var i = 0; i < 200; i++) '+x$i'])));
    expect(_scrollerHeight(tester), greaterThan(300));
    expect(_scrollerHeight(tester), lessThan(600));
  });

  testWidgets('a trailing fold (change at the top) points down', (tester) async {
    await tester.pumpWidget(_wrap(_ud(['-old', '+new', ..._ctx('l', 20)])));
    expect(find.byIcon(Icons.keyboard_double_arrow_down), findsOneWidget);
    expect(find.byIcon(Icons.height), findsNothing);
  });

  testWidgets('a fold between two changes points both ways', (tester) async {
    await tester.pumpWidget(_wrap(_ud([
      ..._ctx('a', 3),
      '-old1',
      '+new1',
      ..._ctx('m', 20),
      '-old2',
      '+new2',
      ..._ctx('z', 3),
    ])));
    expect(find.byIcon(Icons.height), findsOneWidget);
    expect(find.byIcon(Icons.keyboard_double_arrow_down), findsNothing);
  });

  testWidgets('a middle fold anchors the row above it (fills downward)',
      (tester) async {
    // Tail changes keep the content overflowing so scrolling actually matters.
    await tester.pumpWidget(_screen(_ud([
      ..._ctx('a', 3),
      '-old1',
      '+new1',
      for (var i = 0; i < 25; i++) ' mid${i.toString().padLeft(2, '0')}',
      '-old2',
      '+new2',
      for (var i = 0; i < 40; i++) '-ta$i',
      for (var i = 0; i < 40; i++) '+tb$i',
    ])));
    await tester.pump();

    expect(find.byIcon(Icons.height), findsOneWidget);
    final before = tester.getTopLeft(find.textContaining('mid02')).dy;

    await tester.tap(find.byIcon(Icons.height));
    await tester.pumpAndSettle();

    final after = tester.getTopLeft(find.textContaining('mid02')).dy;
    expect(after, closeTo(before, 2.0));
  });
}
