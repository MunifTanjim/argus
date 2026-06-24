import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/ui/chunk_card.dart';

Widget _wrap(Chunk c) => MaterialApp(
    home: Scaffold(
        body: ChunkCard(detailRef: const ToolDetailRef.live('s'), chunk: c)));

void main() {
  testWidgets('user chunk renders its text', (tester) async {
    await tester.pumpWidget(
        _wrap(const Chunk(id: 'u', kind: ChunkKind.user, text: 'fix the bug')));
    expect(find.textContaining('fix the bug'), findsOneWidget);
  });

  testWidgets('ai chunk collapsed shows preview, expands on tap',
      (tester) async {
    const c = Chunk(
      id: 'a',
      kind: ChunkKind.ai,
      model: 'claude-opus-4-8',
      items: [
        Item(id: 'i0', kind: ItemKind.tool, toolName: 'Bash', inputPreview: 'ls'),
        Item(id: 'i1', kind: ItemKind.text, text: 'all done'),
      ],
      hasContext: true,
      contextPct: 42,
      usage: Usage(input: 100, output: 20),
      previewItemId: 'i1',
    );
    await tester.pumpWidget(_wrap(c));
    // collapsed: preview shows the trailing text, tool row hidden
    expect(find.textContaining('all done'), findsOneWidget);
    expect(find.text('Bash'), findsNothing);

    // tapping the collapsed preview expands the card
    await tester.tap(find.textContaining('all done'));
    await tester.pumpAndSettle();
    // expanded: tool row now visible
    expect(find.text('Bash'), findsOneWidget);
  });

  // A chunk with a tool row + trailing text, shared by the toggle-target tests.
  const toggleChunk = Chunk(
    id: 'a',
    kind: ChunkKind.ai,
    model: 'm',
    items: [
      Item(id: 'i0', kind: ItemKind.tool, toolName: 'Bash', inputPreview: 'ls'),
      Item(id: 'i1', kind: ItemKind.text, text: 'all done'),
    ],
    previewItemId: 'i1',
  );

  testWidgets('expanded card collapses via the header chevron', (tester) async {
    await tester.pumpWidget(_wrap(toggleChunk));
    await tester.tap(find.textContaining('all done')); // expand via preview
    await tester.pumpAndSettle();
    expect(find.text('Bash'), findsOneWidget); // expanded

    await tester.tap(find.byIcon(Icons.expand_more)); // collapse via header
    await tester.pumpAndSettle();
    expect(find.text('Bash'), findsNothing); // collapsed
  });

  testWidgets('tapping the expanded body does not collapse the card',
      (tester) async {
    await tester.pumpWidget(_wrap(toggleChunk));
    await tester.tap(find.textContaining('all done')); // expand
    await tester.pumpAndSettle();
    expect(find.text('Bash'), findsOneWidget);

    // a tap on the body content (not the header) must NOT collapse the card
    await tester.tap(find.textContaining('all done').first);
    await tester.pumpAndSettle();
    expect(find.text('Bash'), findsOneWidget); // still expanded
  });

  testWidgets('ai header shows context and tokens', (tester) async {
    const c = Chunk(
      id: 'a',
      kind: ChunkKind.ai,
      model: 'm',
      hasContext: true,
      contextPct: 42,
      usage: Usage(input: 100, output: 20),
    );
    await tester.pumpWidget(_wrap(c));
    expect(find.textContaining('42%'), findsOneWidget);
    expect(find.textContaining('120 tok'), findsOneWidget);
  });

  testWidgets('system chunk shows summary', (tester) async {
    await tester.pumpWidget(_wrap(const Chunk(
        id: 's', kind: ChunkKind.system, summary: 'context compacted')));
    expect(find.textContaining('context compacted'), findsOneWidget);
  });
}
