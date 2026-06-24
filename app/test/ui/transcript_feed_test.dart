import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/ui/transcript_feed.dart';

List<Chunk> _chunks(int n) => [
      for (var i = 0; i < n; i++)
        Chunk(id: 'c$i', kind: ChunkKind.user, text: 'message number $i'),
    ];

Widget _feed(List<Chunk> chunks) => MaterialApp(
      home: Scaffold(
        body: TranscriptFeed(
            key: const ValueKey('feed'), detailRef: const ToolDetailRef.live('s'), chunks: chunks),
      ),
    );

ScrollController _controller(WidgetTester tester) =>
    tester.widget<ListView>(find.byType(ListView)).controller!;

void main() {
  testWidgets('renders chunks', (tester) async {
    await tester.pumpWidget(const MaterialApp(
        home: Scaffold(
            body: TranscriptFeed(detailRef: const ToolDetailRef.live('s'), chunks: [
      Chunk(id: 'u', kind: ChunkKind.user, text: 'hi there'),
    ]))));
    expect(find.textContaining('hi there'), findsOneWidget);
  });

  testWidgets('empty state', (tester) async {
    await tester.pumpWidget(const MaterialApp(
        home: Scaffold(body: TranscriptFeed(detailRef: const ToolDetailRef.live('s'), chunks: []))));
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
}
