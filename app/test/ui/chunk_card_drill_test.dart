import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/ui/chunk_card.dart';
import 'package:argus/ui/item_detail_screen.dart';

void main() {
  testWidgets('tapping an expanded tool row pushes ItemDetailScreen',
      (tester) async {
    const c = Chunk(id: 'a', kind: ChunkKind.ai, modelName: 'm', items: [
      Item(
          id: 'i0',
          kind: ItemKind.tool,
          toolName: 'Bash',
          inputPreview: 'ls',
          toolInput: '{"command":"ls"}',
          result: 'ok'),
      Item(id: 'i1', kind: ItemKind.text, text: 'note'),
    ]);
    await tester.pumpWidget(ProviderScope(
        overrides: [gatewayProvider.overrideWithValue(null)],
        child: const MaterialApp(
            home: Scaffold(
                body: ChunkCard(
                    detailRef: ToolDetailRef.live('s'), chunk: c)))));
    // expand via the header chevron
    await tester.tap(find.byIcon(Icons.chevron_right));
    await tester.pumpAndSettle();
    // drill into the Bash row
    await tester.tap(find.text('Bash'));
    await tester.pumpAndSettle();
    expect(find.byType(ItemDetailScreen), findsOneWidget);
  });
}
