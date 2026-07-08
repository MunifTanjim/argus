import 'package:argus/models/chunk.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/ui/chunk_card.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

// Default prefs (collapseToolCalls = false), so these cover the uncollapsed path.
Widget _wrap(Chunk c) => ProviderScope(
    child: MaterialApp(
        home: Scaffold(
            body: ChunkCard(detailRef: const ToolDetailRef.live('s'), chunk: c))));

void main() {
  testWidgets('user chunk renders its text', (tester) async {
    await tester.pumpWidget(
        _wrap(const Chunk(id: 'u', kind: ChunkKind.user, text: 'fix the bug')));
    expect(find.textContaining('fix the bug'), findsOneWidget);
  });

  testWidgets('shell chunk shows the command, output on expand', (tester) async {
    const c = Chunk(
      id: 'sh',
      kind: ChunkKind.shell,
      text: 'git status',
      detail: 'On branch main\nnothing to commit',
    );
    await tester.pumpWidget(_wrap(c));
    expect(find.text('Shell'), findsOneWidget);
    expect(find.textContaining('git status'), findsOneWidget);
    expect(find.textContaining('nothing to commit'), findsNothing);
    await tester.tap(find.text('Shell'));
    await tester.pump();
    expect(find.textContaining('nothing to commit'), findsOneWidget);
  });

  testWidgets('skill chunk shows the name, path/body on expand', (tester) async {
    const c = Chunk(
      id: 'sk',
      kind: ChunkKind.skill,
      text: 'superpowers:brainstorming',
      label: '/path/to/SKILL.md',
      detail: 'Help turn ideas into designs.',
    );
    await tester.pumpWidget(_wrap(c));
    expect(find.text('Skill'), findsOneWidget);
    expect(find.textContaining('superpowers:brainstorming'), findsOneWidget);
    expect(find.textContaining('SKILL.md'), findsNothing);
    await tester.tap(find.text('Skill'));
    await tester.pump();
    expect(find.textContaining('SKILL.md'), findsOneWidget);
    expect(find.textContaining('Help turn ideas'), findsOneWidget);
  });

  testWidgets('long user chunk collapses and expands', (tester) async {
    final long = List.generate(30, (i) => 'line $i').join('\n');
    // Scrollable parent so tall expanded content doesn't trip an overflow.
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(
            body: SingleChildScrollView(
                child: ChunkCard(
                    detailRef: const ToolDetailRef.live('s'),
                    chunk: Chunk(id: 'u', kind: ChunkKind.user, text: long))))));
    expect(find.text('Show more'), findsOneWidget);
    expect(find.textContaining('line 29'), findsNothing); // hidden while collapsed
    await tester.tap(find.text('Show more'));
    await tester.pump();
    expect(find.text('Show less'), findsOneWidget);
    expect(find.textContaining('line 29'), findsOneWidget);
  });

  testWidgets('ai chunk collapsed shows preview, expands on tap',
      (tester) async {
    const c = Chunk(
      id: 'a',
      kind: ChunkKind.ai,
      modelName: 'Opus 4.8',
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
    modelName: 'm',
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
      modelName: 'm',
      hasContext: true,
      contextPct: 42,
      usage: Usage(input: 100, output: 20),
    );
    await tester.pumpWidget(_wrap(c));
    expect(find.textContaining('42%'), findsOneWidget);
    expect(find.textContaining('120'), findsOneWidget);
  });

  testWidgets('system chunk shows label preview', (tester) async {
    await tester.pumpWidget(_wrap(
        const Chunk(id: 's', kind: ChunkKind.system, label: 'Recap')));
    expect(find.textContaining('Recap'), findsOneWidget);
  });
}
