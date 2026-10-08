import 'package:argus/models/entry.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/ui/prompt_scrollbar.dart';
import 'package:argus/ui/transcript_feed.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:super_sliver_list/super_sliver_list.dart';

FeedRow _user(String id) =>
    EntryFeedRow(Entry(id: id, kind: EntryKind.user, text: id));
FeedRow _text(String id) =>
    EntryFeedRow(Entry(id: id, kind: EntryKind.text, text: id));

/// 60 rows; user prompts at 0, 20 and 40, prose elsewhere.
List<Entry> _transcript() => [
  for (var i = 0; i < 60; i++)
    Entry(
      id: 'e$i',
      kind: i % 20 == 0 ? EntryKind.user : EntryKind.text,
      text: 'line $i',
    ),
];

Widget _feed(List<Entry> entries) => ProviderScope(
  child: MaterialApp(
    home: Scaffold(
      body: TranscriptFeed(
        key: const ValueKey('feed'),
        detailRef: const ToolDetailRef.live('s'),
        entries: entries,
      ),
    ),
  ),
);

SuperListView _list(WidgetTester tester) =>
    tester.widget<SuperListView>(find.byType(SuperListView));

Offset _trackPoint(WidgetTester tester, double fraction) {
  final r = tester.getRect(
    find.byKey(const ValueKey('prompt-scrollbar-track')),
  );
  return Offset(r.center.dx, r.top + r.height * fraction);
}

/// Reveals the scrollbar the way a user does: a small scroll of the list.
Future<void> _reveal(WidgetTester tester) async {
  await tester.drag(find.byType(SuperListView), const Offset(0, 40));
  await tester.pump();
}

Rect _thumb(WidgetTester tester) =>
    tester.getRect(find.byKey(const ValueKey('prompt-scrollbar-thumb')));

void main() {
  group('pure functions', () {
    test('snapIndex takes a custom snap distance', () {
      expect(snapIndex(0.5 + 30 / 400, 400, [0.5], [7]), isNull);
      expect(snapIndex(0.5 + 30 / 400, 400, [0.5], [7], distance: 48), 7);
    });

    test('fisheyeY leaves the focus and far points in place', () {
      expect(fisheyeY(300, 300), 300);
      expect(fisheyeY(300 + kFisheyeRadius, 300), 300 + kFisheyeRadius);
      expect(fisheyeY(10, 300), 10);
    });

    test('fisheyeY spreads points near the focus, monotonically', () {
      final a = fisheyeY(302, 300), b = fisheyeY(306, 300);
      expect(
        b - a,
        greaterThan(3 * 4),
        reason: '4px apart near the focus spread to over 12px',
      );
      expect(fisheyeY(298, 300), lessThan(300));
      var prev = -1e9;
      for (var y = 150.0; y <= 450; y += 0.5) {
        final v = fisheyeY(y, 300);
        expect(v, greaterThanOrEqualTo(prev));
        prev = v;
      }
      // Continuous at the edge of the lens.
      expect(
        fisheyeY(300 + kFisheyeRadius - 0.01, 300),
        closeTo(300 + kFisheyeRadius, 0.1),
      );
    });

    test('bulgeOffset peaks at the focus and fades away from it', () {
      expect(bulgeOffset(300, 300), closeTo(kBulgeAmplitude, 1e-9));
      expect(
        bulgeOffset(300 + kBulgeSigma, 300),
        lessThan(kBulgeAmplitude * 0.7),
      );
      expect(bulgeOffset(300 + 5 * kBulgeSigma, 300), lessThan(0.1));
    });

    test('promptTickFractions places each user row at its index fraction', () {
      final rows = [_user('a'), _text('b'), _text('c'), _text('d'), _user('e')];
      expect(promptTickFractions(rows), [0.0, 1.0]);
      expect(promptRowIndexes(rows), [0, 4]);
      final mid = [_text('a'), _text('b'), _user('c'), _text('d'), _text('e')];
      expect(promptTickFractions(mid), [0.5]);
    });

    test('promptTickFractions with a single row is 0', () {
      expect(promptTickFractions([_user('a')]), [0.0]);
    });

    test('fractionToIndex maps ends and middle', () {
      expect(fractionToIndex(0, 11), 0);
      expect(fractionToIndex(1, 11), 10);
      expect(fractionToIndex(0.5, 11), 5);
      expect(fractionToIndex(-0.2, 11), 0);
      expect(fractionToIndex(1.3, 11), 10);
      expect(fractionToIndex(0.5, 1), 0);
    });

    test('snapIndex snaps within the band and not outside it', () {
      // Track 400px; tick at 0.5 = 200px.
      expect(snapIndex(0.5 + 11 / 400, 400, [0.5], [7]), 7);
      expect(snapIndex(0.5 - 11 / 400, 400, [0.5], [7]), 7);
      expect(snapIndex(0.5 + 13 / 400, 400, [0.5], [7]), isNull);
      expect(kPromptSnapDistance, 12);
    });

    test('snapIndex picks the nearest of two close ticks', () {
      // Ticks at 200px and 210px; pointer at 207px.
      expect(snapIndex(207 / 400, 400, [0.5, 210 / 400], [3, 4]), 4);
      expect(snapIndex(203 / 400, 400, [0.5, 210 / 400], [3, 4]), 3);
    });
  });

  group('widget', () {
    testWidgets('hidden when content fits', (tester) async {
      await tester.pumpWidget(
        _feed([
          const Entry(id: 'u', kind: EntryKind.user, text: 'hi'),
          const Entry(id: 't', kind: EntryKind.text, text: 'yo'),
        ]),
      );
      await tester.pumpAndSettle();
      expect(
        find.byKey(const ValueKey('prompt-scrollbar-track')),
        findsNothing,
      );
    });

    testWidgets('renders a tick for each prompt', (tester) async {
      await tester.pumpWidget(_feed(_transcript()));
      await tester.pumpAndSettle();
      expect(
        find.byKey(const ValueKey('prompt-scrollbar-track')),
        findsOneWidget,
      );
      for (final id in ['e0', 'e20', 'e40']) {
        expect(find.byKey(ValueKey('prompt-tick-$id')), findsOneWidget);
      }
      expect(find.byKey(const ValueKey('prompt-tick-e1')), findsNothing);
    });

    testWidgets('dragging near a tick lands that prompt at the top', (
      tester,
    ) async {
      await tester.pumpWidget(_feed(_transcript()));
      await tester.pumpAndSettle();
      await _reveal(tester);

      final r = tester.getRect(
        find.byKey(const ValueKey('prompt-scrollbar-track')),
      );
      // Tick for e20 sits at 20/59 of the track; land 8px below it.
      final g = await tester.startGesture(_trackPoint(tester, 0.9));
      await tester.pump();
      await g.moveTo(Offset(r.center.dx, r.top + r.height * 20 / 59 + 8));
      await tester.pump();
      await g.up();
      await tester.pumpAndSettle();

      expect(_list(tester).listController!.visibleRange!.$1, 20);
      final top = tester.getTopLeft(find.byKey(const ValueKey('e20'))).dy;
      expect(top, closeTo(r.top, 1));
    });

    testWidgets('dragging to the bottom resumes following', (tester) async {
      await tester.pumpWidget(_feed(_transcript()));
      await tester.pumpAndSettle();
      await _reveal(tester);

      final g = await tester.startGesture(_trackPoint(tester, 0.1));
      await tester.pump();
      final sc = _list(tester).controller!;
      expect(sc.offset, lessThan(sc.position.maxScrollExtent - 100));
      await g.moveTo(_trackPoint(tester, 1));
      await tester.pump();
      await g.up();
      await tester.pumpAndSettle();
      expect(sc.offset, closeTo(sc.position.maxScrollExtent, 1));

      await tester.pumpWidget(
        _feed([
          ..._transcript(),
          const Entry(id: 'new', kind: EntryKind.text, text: 'appended'),
        ]),
      );
      await tester.pumpAndSettle();
      expect(sc.offset, closeTo(sc.position.maxScrollExtent, 1));
      expect(find.byKey(const ValueKey('new')), findsOneWidget);
    });

    testWidgets('dragging up stops following', (tester) async {
      await tester.pumpWidget(_feed(_transcript()));
      await tester.pumpAndSettle();
      await _reveal(tester);

      final g = await tester.startGesture(_trackPoint(tester, 0.6));
      await tester.pump();
      await g.up();
      await tester.pumpAndSettle();
      final sc = _list(tester).controller!;
      final before = sc.offset;
      expect(before, lessThan(sc.position.maxScrollExtent - 100));

      await tester.pumpWidget(
        _feed([
          ..._transcript(),
          const Entry(id: 'new', kind: EntryKind.text, text: 'appended'),
        ]),
      );
      await tester.pumpAndSettle();
      expect(sc.offset, closeTo(before, 1));
    });

    testWidgets('fades out after inactivity', (tester) async {
      await tester.pumpWidget(_feed(_transcript()));
      await tester.pumpAndSettle();
      double opacity() => tester
          .widget<AnimatedOpacity>(
            find.byKey(const ValueKey('prompt-scrollbar')),
          )
          .opacity;

      await _reveal(tester);
      final g = await tester.startGesture(_trackPoint(tester, 0.5));
      await tester.pump();
      expect(opacity(), 1);
      await g.up();
      await tester.pump(const Duration(milliseconds: 1000));
      expect(opacity(), 1);
      await tester.pump(const Duration(milliseconds: 600));
      expect(opacity(), 0);
    });

    testWidgets('ignores touches while hidden', (tester) async {
      await tester.pumpWidget(_feed(_transcript()));
      await tester.pumpAndSettle();
      // A slow swipe starting near the top of the hidden track. Were the
      // scrollbar to take it, the list would jump to the top rows.
      final g = await tester.startGesture(_trackPoint(tester, 0.05));
      for (var i = 0; i < 10; i++) {
        await g.moveBy(const Offset(0, 10));
        await tester.pump(const Duration(milliseconds: 50));
      }
      await tester.pump(const Duration(milliseconds: 200)); // hold: no fling
      await g.up();
      await tester.pumpAndSettle();
      final first = _list(tester).listController!.visibleRange!.$1;
      expect(
        first,
        greaterThan(30),
        reason: 'the swipe scrolled the list a little; it did not jump',
      );
      final sc = _list(tester).controller!;
      expect(
        sc.offset,
        lessThan(sc.position.maxScrollExtent),
        reason: 'the swipe did scroll the list',
      );
    });

    testWidgets('thumb follows the finger while dragging', (tester) async {
      await tester.pumpWidget(_feed(_transcript()));
      await tester.pumpAndSettle();
      await _reveal(tester);
      final g = await tester.startGesture(_trackPoint(tester, 0.5));
      await tester.pump();
      final at = _trackPoint(tester, 0.55); // far from any tick
      await g.moveTo(at);
      await tester.pump();
      expect(_thumb(tester).center.dy, closeTo(at.dy, 1));
      await g.up();
    });

    testWidgets('a snapped thumb sits on its tick', (tester) async {
      await tester.pumpWidget(_feed(_transcript()));
      await tester.pumpAndSettle();
      await _reveal(tester);
      final tick = tester
          .getRect(find.byKey(const ValueKey('prompt-tick-e20')))
          .center;
      final g = await tester.startGesture(_trackPoint(tester, 0.9));
      await tester.pump();
      await g.moveTo(tick + const Offset(0, 8));
      await tester.pump();
      expect(
        _thumb(tester).center.dy,
        closeTo(tick.dy, 1),
        reason: 'while snapped',
      );
      await g.up();
      await tester.pumpAndSettle();
      expect(
        _thumb(tester).center.dy,
        closeTo(tick.dy, 1),
        reason: 'after release, the list sits at that prompt',
      );
    });

    testWidgets('the track bends and spreads ticks around the finger', (
      tester,
    ) async {
      await tester.pumpWidget(_feed(_transcript()));
      await tester.pumpAndSettle();
      await _reveal(tester);
      Rect tick(String id) =>
          tester.getRect(find.byKey(ValueKey('prompt-tick-$id')));
      final near0 = tick('e20'), far0 = tick('e0');

      // Touch 6px above e20's tick: it's inside the lens and the bulge.
      final g = await tester.startGesture(near0.center - const Offset(0, 6));
      await tester.pump(); // the touch registers; the bend starts
      await tester.pump(const Duration(milliseconds: 300)); // and eases in
      final near = tick('e20'), far = tick('e0');
      final finger = near0.center.dy - 6;
      expect(
        (near.center.dy - finger).abs(),
        greaterThan(6 + 4),
        reason: 'a tick near the finger spreads away from it',
      );
      expect(
        near0.left - near.left,
        greaterThan(kBulgeAmplitude * 0.5),
        reason: 'a tick near the finger bends out with the track',
      );
      expect(
        far.center.dy,
        closeTo(far0.center.dy, 0.5),
        reason: 'ticks outside the lens stay put',
      );
      expect(far.left, closeTo(far0.left, 0.5));
      expect(
        _thumb(tester).left,
        lessThan(near0.left),
        reason: 'the thumb bends out beside the finger',
      );

      await g.up();
      await tester.pumpAndSettle();
      expect(
        tick('e20').left,
        closeTo(near0.left, 0.5),
        reason: 'the track straightens after release',
      );
    });

    testWidgets('prompt marks are small round dots', (tester) async {
      await tester.pumpWidget(_feed(_transcript()));
      await tester.pumpAndSettle();
      final mark = find.byKey(const ValueKey('prompt-tick-e20'));
      final r = tester.getRect(mark);
      expect(r.width, r.height);
      expect(r.width, lessThanOrEqualTo(8));
      final box = tester.widget<DecoratedBox>(
        find.descendant(of: mark, matching: find.byType(DecoratedBox)),
      );
      expect((box.decoration as BoxDecoration).shape, BoxShape.circle);
    });

    testWidgets('snap reach grows with the fisheye', (tester) async {
      await tester.pumpWidget(_feed(_transcript()));
      await tester.pumpAndSettle();
      await _reveal(tester);
      final r = tester.getRect(
        find.byKey(const ValueKey('prompt-scrollbar-track')),
      );
      final tickY = r.top + r.height * 20 / 59;
      Future<int> landAt(double dy) async {
        await _reveal(tester);
        final g = await tester.startGesture(_trackPoint(tester, 0.95));
        await tester.pump();
        await g.moveTo(Offset(r.center.dx, tickY + dy));
        await tester.pump();
        await g.up();
        await tester.pumpAndSettle();
        return _list(tester).listController!.visibleRange!.$1;
      }

      // 16px real is past the plain 12px reach but inside the magnified one.
      expect(await landAt(16), 20);
      // Far enough that even the magnified reach lets go.
      expect(await landAt(30), isNot(20));
    });
  });
}
