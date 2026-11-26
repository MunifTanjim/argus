import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/entry.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/ui/transcript_feed.dart';

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
    tester.widget<CustomScrollView>(find.byType(CustomScrollView)).controller!;

void main() {
  testWidgets('a turn footer shows the model only when it changes',
      (tester) async {
    await tester.pumpWidget(const ProviderScope(
        child: MaterialApp(
            home: Scaffold(
                body: TranscriptFeed(
                    detailRef: ToolDetailRef.live('s'),
                    stickToBottom: false,
                    entries: [
          Entry(id: 'u1', kind: EntryKind.user, text: 'one'),
          Entry(id: 'n1', kind: EntryKind.turnEnd, modelName: 'Model A', durationMs: 1000),
          Entry(id: 'u2', kind: EntryKind.user, text: 'two'),
          Entry(id: 'n2', kind: EntryKind.turnEnd, modelName: 'Model A', durationMs: 2000),
          Entry(id: 'u3', kind: EntryKind.user, text: 'three'),
          Entry(id: 'n3', kind: EntryKind.turnEnd, modelName: 'Model B', durationMs: 3000),
        ])))));
    await tester.pumpAndSettle();
    expect(find.textContaining('Model A'), findsOneWidget);
    expect(find.textContaining('Model B'), findsOneWidget);
    expect(find.textContaining('2.0s'), findsOneWidget); // the second footer stays
  });

  testWidgets('a feed that empties reports the header as visible',
      (tester) async {
    final seen = <bool>[];
    Widget feed(List<Entry> entries) => ProviderScope(
        child: MaterialApp(
            home: Scaffold(
                body: TranscriptFeed(
                    key: const ValueKey('feed'),
                    detailRef: const ToolDetailRef.live('s'),
                    entries: entries,
                    header: const SizedBox(height: 80, child: Text('HEADER')),
                    onHeaderVisible: seen.add))));
    await tester.pumpWidget(feed(_chunks(50)));
    await tester.pumpAndSettle();
    expect(seen.last, isFalse);
    await tester.pumpWidget(feed(const []));
    await tester.pumpAndSettle();
    expect(seen.last, isTrue);
  });

  testWidgets('a following feed tails the live row when work starts',
      (tester) async {
    final entries = [
      ..._chunks(50),
      const Entry(id: 'a', kind: EntryKind.tool, toolName: 'ZZAlpha'),
      const Entry(id: 'b', kind: EntryKind.tool, toolName: 'ZZBeta'),
    ];
    Widget feed(bool live) => ProviderScope(
        child: MaterialApp(
            home: Scaffold(
                body: TranscriptFeed(
                    key: const ValueKey('feed'),
                    detailRef: const ToolDetailRef.live('s'),
                    entries: entries,
                    live: live))));
    await tester.pumpWidget(feed(false));
    await tester.pumpAndSettle();
    await tester.pumpWidget(feed(true));
    await tester.pumpAndSettle();
    final p = _controller(tester).position;
    expect(p.pixels, closeTo(p.maxScrollExtent, 1));
    expect(find.text('ZZBeta').hitTestable(), findsOneWidget);
  });

  testWidgets('a bottom inset adds room below the last row', (tester) async {
    Widget feed(double inset) => ProviderScope(
        child: MaterialApp(
            home: Scaffold(
                body: TranscriptFeed(
                    key: const ValueKey('feed'),
                    detailRef: const ToolDetailRef.live('s'),
                    entries: _chunks(50),
                    bottomInset: inset))));
    await tester.pumpWidget(feed(0));
    await tester.pumpAndSettle();
    final without = _controller(tester).position.maxScrollExtent;
    await tester.pumpWidget(feed(100));
    await tester.pumpAndSettle();
    expect(_controller(tester).position.maxScrollExtent,
        closeTo(without + 100, 1));
  });

  testWidgets('a feed at the bottom stays there when the inset grows',
      (tester) async {
    Widget feed(double inset) => ProviderScope(
        child: MaterialApp(
            home: Scaffold(
                body: TranscriptFeed(
                    key: const ValueKey('feed'),
                    detailRef: const ToolDetailRef.live('s'),
                    entries: _chunks(50),
                    bottomInset: inset))));
    await tester.pumpWidget(feed(0));
    await tester.pumpAndSettle();
    await tester.pumpWidget(feed(100));
    await tester.pumpAndSettle();
    final p = _controller(tester).position;
    expect(p.pixels, closeTo(p.maxScrollExtent, 1));
  });

  testWidgets('a header shows above the first row', (tester) async {
    await tester.pumpWidget(ProviderScope(
        child: MaterialApp(
            home: Scaffold(
                body: TranscriptFeed(
                    detailRef: const ToolDetailRef.live('s'),
                    entries: _chunks(2),
                    header: const Text('HEADER'))))));
    expect(
        tester.getTopLeft(find.text('HEADER')).dy,
        lessThan(
            tester.getTopLeft(find.textContaining('message number 0')).dy));
  });

  testWidgets('header visibility follows the scroll offset', (tester) async {
    final seen = <bool>[];
    await tester.pumpWidget(ProviderScope(
        child: MaterialApp(
            home: Scaffold(
                body: TranscriptFeed(
                    detailRef: const ToolDetailRef.live('s'),
                    entries: _chunks(50),
                    header: const SizedBox(height: 80, child: Text('HEADER')),
                    onHeaderVisible: seen.add)))));
    await tester.pumpAndSettle();
    expect(seen.last, isFalse); // opens at the bottom
    _controller(tester).jumpTo(0);
    await tester.pump();
    expect(seen.last, isTrue);
  });

  testWidgets('the empty state shows the header', (tester) async {
    await tester.pumpWidget(const ProviderScope(
        child: MaterialApp(
            home: Scaffold(
                body: TranscriptFeed(
                    detailRef: ToolDetailRef.live('s'),
                    entries: [],
                    header: Text('HEADER'))))));
    expect(find.text('HEADER'), findsOneWidget);
    expect(find.textContaining('No transcript'), findsOneWidget);
  });

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
    debugPrint("DBG start off=${c.offset} max=${c.position.maxScrollExtent}");

    await tester.pumpWidget(sized(200)); // the keyboard opens
    expect(c.offset + 200, closeTo(bottom, 1));

    debugPrint("DBG after shrink off=${c.offset} max=${c.position.maxScrollExtent}");
    await tester.pumpWidget(sized(500)); // the keyboard closes
    debugPrint("DBG after grow off=${c.offset} max=${c.position.maxScrollExtent}");
    expect(c.offset + 500, closeTo(bottom, 1));
  });

  testWidgets('a static feed stays at the top when the viewport shrinks',
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
                      entries: _chunks(50),
                      stickToBottom: false),
                ),
              ),
            ),
          ),
        );
    await tester.pumpWidget(sized(500));
    await tester.pumpAndSettle();
    expect(_controller(tester).offset, 0);

    await tester.pumpWidget(sized(200));
    expect(_controller(tester).offset, 0);
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

  testWidgets('follow tails again when far from the bottom', (tester) async {
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

    expect(_controller(tester).offset,
        closeTo(_controller(tester).position.maxScrollExtent, 1));
  });

  testWidgets('rows are keyed by entry id', (tester) async {
    await tester.pumpWidget(_feed(_chunks(3)));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('c1')), findsOneWidget);
  });
}
