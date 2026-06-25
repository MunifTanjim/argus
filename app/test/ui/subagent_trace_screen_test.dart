import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/ui/subagent_trace_screen.dart';

void main() {
  testWidgets('renders an inline trace without subscribing', (tester) async {
    const item = Item(
      id: 'i',
      kind: ItemKind.subagent,
      subagentType: 'Explore',
      hasTrace: true,
      trace: [
        Chunk(id: 't', kind: ChunkKind.ai, previewItemId: 'ti', items: [
          Item(id: 'ti', kind: ItemKind.text, text: 'searched everything'),
        ]),
      ],
    );
    await tester.pumpWidget(ProviderScope(
      overrides: [gatewayProvider.overrideWithValue(null)],
      child: const MaterialApp(
          home: SubagentTraceScreen(
              parentRef: ToolDetailRef.live('s'), item: item)),
    ));
    await tester.pump();
    expect(find.text('Explore'), findsWidgets);
    expect(find.textContaining('searched everything'), findsOneWidget);
  });
}
