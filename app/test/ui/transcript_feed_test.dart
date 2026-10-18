import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/entry.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/ui/transcript_feed.dart';
import 'package:super_sliver_list/super_sliver_list.dart';

List<Entry> _chunks(int n) => [
      for (var i = 0; i < n; i++)
        Entry(id: 'c$i', kind: EntryKind.user, text: 'message number $i'),
    ];

Widget _feed(List<Entry> chunks) => ProviderScope(
      child: MaterialApp(
        home: Scaffold(
          body: TranscriptFeed(
              key: const ValueKey('feed'), detailRef: const ToolDetailRef.live('s'), entries: chunks),
        ),
      ),
    );

ScrollController _controller(WidgetTester tester) =>
    tester.widget<SuperListView>(find.byType(SuperListView)).controller!;

void main() {
  testWidgets('renders chunks', (tester) async {
    await tester.pumpWidget(const ProviderScope(
        child: MaterialApp(
            home: Scaffold(
                body: TranscriptFeed(detailRef: ToolDetailRef.live('s'), entries: [
      Entry(id: 'u', kind: EntryKind.user, text: 'hi there'),
    ])))));
    expect(find.textContaining('hi there'), findsOneWidget);
  });

  testWidgets('empty state', (tester) async {
    await tester.pumpWidget(const ProviderScope(
        child: MaterialApp(
            home: Scaffold(body: TranscriptFeed(detailRef: ToolDetailRef.live('s'), entries: [])))));
    expect(find.textContaining('No transcript'), findsOneWidget);
  });

  testWidgets('opens scrolled to the bottom', (tester) async {
    await tester.pumpWidget(_feed(_chunks(50)));
    await tester.pumpAndSettle();

    final c = _controller(tester);
    expect(c.position.maxScrollExtent, greaterThan(0));
    expect(c.offset, closeTo(c.position.maxScrollExtent, 1));
  });

  testWidgets('follows new items when at the bottom', (tester) async {
    await tester.pumpWidget(_feed(_chunks(50)));
    await tester.pumpAndSettle();

    await tester.pumpWidget(_feed(_chunks(51))); // a new item arrives
    await tester.pumpAndSettle();

    final c = _controller(tester);
    expect(c.offset, closeTo(c.position.maxScrollExtent, 1),
        reason: 'should tail to the new bottom');
  });

  testWidgets('does not follow when scrolled up', (tester) async {
    await tester.pumpWidget(_feed(_chunks(50)));
    await tester.pumpAndSettle();

    final c = _controller(tester);
    c.jumpTo(0); // user scrolls to the top
    await tester.pump();
    expect(c.position.maxScrollExtent, greaterThan(0));

    await tester.pumpWidget(_feed(_chunks(51))); // a new item arrives
    await tester.pumpAndSettle();

    expect(_controller(tester).offset, closeTo(0, 1),
        reason: 'scrolled-up view must stay put');
  });

  testWidgets('a shorter viewport keeps the bottom edge in place',
      (tester) async {
    Widget sized(double height) => ProviderScope(
          child: MaterialApp(
            home: Scaffold(
              body: Align(
                alignment: Alignment.topCenter,
                child: SizedBox(
                  height: height,
                  child: TranscriptFeed(
                      detailRef: const ToolDetailRef.live('s'),
                      entries: _chunks(50)),
                ),
              ),
            ),
          ),
        );
    await tester.pumpWidget(sized(500));
    await tester.pumpAndSettle();

    final c = _controller(tester);
    c.jumpTo(c.position.maxScrollExtent / 2);
    await tester.pump();
    final bottom = c.offset + 500;

    await tester.pumpWidget(sized(200)); // the keyboard opens
    expect(c.offset + 200, closeTo(bottom, 1));

    await tester.pumpWidget(sized(500)); // the keyboard closes
    expect(c.offset + 500, closeTo(bottom, 1));
  });

  testWidgets('follow tails again when near the bottom', (tester) async {
    await tester.pumpWidget(_feed(_chunks(50)));
    await tester.pumpAndSettle();

    final c = _controller(tester);
    c.jumpTo(c.position.maxScrollExtent - c.position.viewportDimension / 4);
    await tester.pump();

    tester
        .state<TranscriptFeedState>(find.byKey(const ValueKey('feed')))
        .follow();
    await tester.pumpWidget(_feed(_chunks(51))); // the sent prompt arrives
    await tester.pumpAndSettle();

    expect(_controller(tester).offset,
        closeTo(_controller(tester).position.maxScrollExtent, 1));
  });

  testWidgets('follow stays put when far from the bottom', (tester) async {
    await tester.pumpWidget(_feed(_chunks(50)));
    await tester.pumpAndSettle();

    final c = _controller(tester);
    c.jumpTo(0);
    await tester.pump();

    tester
        .state<TranscriptFeedState>(find.byKey(const ValueKey('feed')))
        .follow();
    await tester.pumpWidget(_feed(_chunks(51))); // the sent prompt arrives
    await tester.pumpAndSettle();

    expect(_controller(tester).offset, closeTo(0, 1));
  });

  testWidgets('rows are keyed by entry id', (tester) async {
    await tester.pumpWidget(_feed(_chunks(3)));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('c1')), findsOneWidget);
  });
}
