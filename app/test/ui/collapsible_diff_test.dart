import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/ui/edit_diff.dart';

// The diff scrolls its own content and pins its header, so it needs a
// bounded-height parent (the Scaffold body) rather than an outer scroll view.
Widget _wrap(String oldS, String newS) => MaterialApp(
      home: Scaffold(body: collapsibleDiffView(oldS, newS)),
    );

// Mirrors the review screen: a static line above a Flexible diff, so a short
// diff stays compact and a long one caps at the viewport.
Widget _screen(String oldS, String newS) => MaterialApp(
      home: Scaffold(
        body: Column(
          children: [
            const Text('header'),
            Flexible(child: collapsibleDiffView(oldS, newS)),
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
    // 20 unchanged lines, then a one-line change at the bottom.
    final ctx = List.generate(20, (i) => 'l$i').join('\n');
    await tester.pumpWidget(_wrap('$ctx\nold\n', '$ctx\nnew\n'));

    // The change and its nearby context are visible…
    expect(find.textContaining('- old'), findsOneWidget);
    expect(find.textContaining('+ new'), findsOneWidget);
    expect(find.textContaining('l19'), findsOneWidget); // within 3 of change
    // …while far context is folded behind an expand bar.
    expect(find.textContaining('Expand'), findsOneWidget);
    expect(find.textContaining('l0\n'), findsNothing);
    expect(find.text('  l0'), findsNothing);

    // Expand all reveals the whole file.
    await tester.tap(find.byIcon(Icons.unfold_more));
    await tester.pump();
    expect(find.textContaining('l0'), findsWidgets);
    expect(find.textContaining('Expand'), findsNothing);
  });

  testWidgets('tapping an expand bar reveals the whole folded run',
      (tester) async {
    final ctx = List.generate(20, (i) => 'l$i').join('\n');
    await tester.pumpWidget(_wrap('$ctx\nold\n', '$ctx\nnew\n'));

    // 17 lines (l0..l16) are folded; l17..l19 stay as context near the change.
    expect(find.textContaining('Expand 17 unchanged lines'), findsOneWidget);

    await tester.tap(find.textContaining('Expand'));
    await tester.pump();

    // One tap reveals the entire run, not a single line, and leaves no bar.
    expect(find.text('  l0'), findsOneWidget);
    expect(find.text('  l16'), findsOneWidget);
    expect(find.textContaining('Expand'), findsNothing);
  });

  testWidgets('empty old side renders an all-additions view', (tester) async {
    await tester.pumpWidget(_wrap('', 'a\nb\nc\n'));
    expect(find.textContaining('+ a'), findsOneWidget);
    expect(find.textContaining('+ b'), findsOneWidget);
    expect(find.textContaining('+ c'), findsOneWidget);
    expect(find.textContaining('Expand'), findsNothing);
  });

  testWidgets('empty new side renders an all-deletions view', (tester) async {
    await tester.pumpWidget(_wrap('x\ny\n', ''));
    expect(find.textContaining('- x'), findsOneWidget);
    expect(find.textContaining('- y'), findsOneWidget);
  });

  testWidgets('a short diff stays compact instead of filling the viewport',
      (tester) async {
    // Two rows in an 800×600 test surface: the scroller hugs its content.
    await tester.pumpWidget(_screen('a', 'b'));
    expect(_scrollerHeight(tester), lessThan(200));
  });

  testWidgets('a tall diff caps at the viewport and scrolls', (tester) async {
    final many = List.generate(200, (i) => 'x$i').join('\n');
    await tester.pumpWidget(_screen('', '$many\n'));
    // Capped near the available height rather than the ~3000px of content.
    expect(_scrollerHeight(tester), greaterThan(300));
    expect(_scrollerHeight(tester), lessThan(600));
  });

  testWidgets('a leading fold (change at the bottom) points up', (tester) async {
    final ctx = List.generate(20, (i) => 'l$i').join('\n');
    await tester.pumpWidget(_wrap('$ctx\nold\n', '$ctx\nnew\n'));
    expect(find.byIcon(Icons.keyboard_double_arrow_up), findsOneWidget);
    expect(find.byIcon(Icons.keyboard_double_arrow_down), findsNothing);
    expect(find.byIcon(Icons.height), findsNothing);
  });

  testWidgets('a trailing fold (change at the top) points down', (tester) async {
    final ctx = List.generate(20, (i) => 'l$i').join('\n');
    await tester.pumpWidget(_wrap('old\n$ctx\n', 'new\n$ctx\n'));
    expect(find.byIcon(Icons.keyboard_double_arrow_down), findsOneWidget);
    expect(find.byIcon(Icons.keyboard_double_arrow_up), findsNothing);
    expect(find.byIcon(Icons.height), findsNothing);
  });

  testWidgets('a fold between two changes points both ways', (tester) async {
    final mid = List.generate(20, (i) => 'm$i').join('\n');
    await tester.pumpWidget(_wrap(
      'a\nb\nc\nold1\n$mid\nold2\nx\ny\nz\n',
      'a\nb\nc\nnew1\n$mid\nnew2\nx\ny\nz\n',
    ));
    expect(find.byIcon(Icons.height), findsOneWidget);
    expect(find.byIcon(Icons.keyboard_double_arrow_up), findsNothing);
    expect(find.byIcon(Icons.keyboard_double_arrow_down), findsNothing);
  });

  testWidgets('a middle fold anchors the row above it (fills downward)',
      (tester) async {
    final mid =
        List.generate(25, (i) => 'mid${i.toString().padLeft(2, '0')}').join('\n');
    // Tail changes keep the content overflowing so scrolling actually matters.
    final tailOld = List.generate(40, (i) => 'ta$i').join('\n');
    final tailNew = List.generate(40, (i) => 'tb$i').join('\n');
    await tester.pumpWidget(_screen(
      'a\nb\nc\nold1\n$mid\nold2\n$tailOld\n',
      'a\nb\nc\nnew1\n$mid\nnew2\n$tailNew\n',
    ));
    await tester.pump();

    expect(find.byIcon(Icons.height), findsOneWidget);
    final before = tester.getTopLeft(find.textContaining('mid02')).dy;

    await tester.tap(find.byIcon(Icons.height));
    await tester.pumpAndSettle();

    // The row above the fold stays put; the revealed lines fill downward.
    final after = tester.getTopLeft(find.textContaining('mid02')).dy;
    expect(after, closeTo(before, 2.0));
  });

  testWidgets('expanding an upward run anchors the viewport, not the file top',
      (tester) async {
    final before = List.generate(40, (i) => 'c$i').join('\n');
    final after = List.generate(10, (i) => 't$i').join('\n');
    await tester.pumpWidget(
        _screen('$before\nold\n$after\n', '$before\nnew\n$after\n'));
    await tester.pump();

    double offset() =>
        tester.state<ScrollableState>(find.byType(Scrollable).first)
            .position
            .pixels;

    // The change is visible and the top run is folded; nothing has scrolled.
    expect(offset(), 0);
    expect(find.textContaining('+ new'), findsOneWidget);

    // Reveal the folded lines above the change (the first of the two fold bars).
    await tester.tap(find.textContaining('Expand').first);
    await tester.pumpAndSettle();

    // We scrolled down to keep the change in view instead of snapping to c0.
    expect(offset(), greaterThan(0));
    expect(find.textContaining('+ new'), findsOneWidget);
  });

  testWidgets('expand-to-top keeps the row below the bar exactly in place',
      (tester) async {
    // A foldable head, then enough changed lines to fill (and overflow) the
    // viewport, so the top fold bar sits at the very top with l17 just below it.
    final lead = List.generate(20, (i) => 'l$i').join('\n');
    final olds = List.generate(60, (i) => 'old$i').join('\n');
    final news = List.generate(60, (i) => 'new$i').join('\n');
    await tester.pumpWidget(_screen('$lead\n$olds\n', '$lead\n$news\n'));
    await tester.pump();

    expect(find.byIcon(Icons.keyboard_double_arrow_up), findsOneWidget);
    final before = tester.getTopLeft(find.textContaining('l17')).dy;

    await tester.tap(find.byIcon(Icons.keyboard_double_arrow_up));
    await tester.pumpAndSettle();

    // l17 must not shift: the revealed lines fill upward into the bar's slot,
    // rather than l17 jumping up to where the bar was.
    final after = tester.getTopLeft(find.textContaining('l17')).dy;
    expect(after, closeTo(before, 2.0));
  });
}
