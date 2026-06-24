import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/ui/item_detail_screen.dart';

/// A ToolDetailApi that returns a fixed body without any RPC.
class _FakeToolDetailApi implements ToolDetailApi {
  _FakeToolDetailApi(this._detail);
  final ToolDetail _detail;
  int calls = 0;

  @override
  Future<Result<ToolDetail>> fetch(ToolDetailRef ref, String toolId) async {
    calls++;
    return Result.ok(_detail);
  }
}

void main() {
  testWidgets('renders an already-populated tool body without fetching',
      (tester) async {
    final fake = _FakeToolDetailApi(const ToolDetail());
    await tester.pumpWidget(ProviderScope(
      overrides: [toolDetailApiProvider.overrideWithValue(fake)],
      child: const MaterialApp(
        home: ItemDetailScreen(
          detailRef: ToolDetailRef.live('s'),
          item: Item(
            id: 'i',
            kind: ItemKind.tool,
            toolName: 'Bash',
            toolInput: '{"command":"echo hi"}',
            result: 'hi',
          ),
        ),
      ),
    ));
    await tester.pumpAndSettle();
    expect(find.text('Bash'), findsOneWidget);
    expect(find.textContaining('echo hi'), findsOneWidget);
    expect(fake.calls, 0, reason: 'inline body must not trigger a fetch');
  });

  testWidgets('fetches the tool body on open when stripped', (tester) async {
    final fake = _FakeToolDetailApi(
        const ToolDetail(toolInput: '{"command":"echo hi"}', result: 'hi'));
    await tester.pumpWidget(ProviderScope(
      overrides: [toolDetailApiProvider.overrideWithValue(fake)],
      child: const MaterialApp(
        home: ItemDetailScreen(
          detailRef: ToolDetailRef.live('s'),
          item: Item(id: 'i', kind: ItemKind.tool, toolName: 'Bash', toolId: 'T1'),
        ),
      ),
    ));
    await tester.pumpAndSettle();
    expect(fake.calls, 1, reason: 'stripped body must be fetched on open');
    expect(find.textContaining('echo hi'), findsOneWidget);
  });
}
