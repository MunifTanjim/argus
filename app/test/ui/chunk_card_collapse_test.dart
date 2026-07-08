import 'package:argus/models/chunk.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/appearance.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/ui/chunk_card.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeKv implements SecureKv {
  _FakeKv(this._m);
  final Map<String, String> _m;
  @override
  Future<String?> read(String key) async => _m[key];
  @override
  Future<void> write(String key, String value) async => _m[key] = value;
  @override
  Future<void> delete(String key) async => _m.remove(key);
}

// Tool names below are absent from the tool registry so ItemRow falls back to
// rendering the raw name, giving the tests a stable string to find.

Chunk _oneTool() => const Chunk(
      id: 'c1',
      kind: ChunkKind.ai,
      items: [
        Item(id: 'i0', kind: ItemKind.text, text: 'doing the thing'),
        Item(id: 'i1', kind: ItemKind.tool, toolName: 'ZZWidgetTool'),
      ],
    );

Chunk _twoGroups() => const Chunk(
      id: 'c2',
      kind: ChunkKind.ai,
      items: [
        Item(id: 'i0', kind: ItemKind.text, text: 'first'),
        Item(id: 'i1', kind: ItemKind.tool, toolName: 'ZZAlpha'),
        Item(id: 'i2', kind: ItemKind.text, text: 'second'),
        Item(id: 'i3', kind: ItemKind.tool, toolName: 'ZZBeta'),
        Item(id: 'i4', kind: ItemKind.tool, toolName: 'ZZGamma'),
      ],
    );

Chunk _blankBetween() => const Chunk(
      id: 'c3',
      kind: ChunkKind.ai,
      items: [
        Item(id: 'i0', kind: ItemKind.tool, toolName: 'ZZAlpha'),
        Item(id: 'i1', kind: ItemKind.text, text: '   '),
        Item(id: 'i2', kind: ItemKind.tool, toolName: 'ZZBeta'),
      ],
    );

Future<void> _pump(
  WidgetTester tester, {
  required bool collapse,
  required Chunk chunk,
}) async {
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        appearanceStoreProvider.overrideWithValue(
          AppearanceStore(_FakeKv({
            'appearance.collapseToolCalls': collapse ? 'true' : 'false',
          })),
        ),
      ],
      child: MaterialApp(
        home: Scaffold(
          body: ChunkCard(
            detailRef: const ToolDetailRef(sessionId: 's1'),
            chunk: chunk,
          ),
        ),
      ),
    ),
  );
  // Let the async pref hydration settle.
  await tester.pumpAndSettle();
  // Expand the AI chunk; its collapsed header reads "response" with no model meta.
  await tester.tap(find.text('response'));
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('shows tool row inline when collapse pref is off', (tester) async {
    await _pump(tester, collapse: false, chunk: _oneTool());
    expect(find.text('ZZWidgetTool'), findsOneWidget);
    expect(find.textContaining('tool call'), findsNothing);
  });

  testWidgets('hides a tool row behind a hint when collapse pref is on',
      (tester) async {
    await _pump(tester, collapse: true, chunk: _oneTool());
    expect(find.text('ZZWidgetTool'), findsNothing);
    expect(find.textContaining('1 tool call'), findsOneWidget);
  });

  testWidgets('tapping the hint reveals the tool row and offers to hide it',
      (tester) async {
    await _pump(tester, collapse: true, chunk: _oneTool());
    await tester.tap(find.textContaining('1 tool call'));
    await tester.pumpAndSettle();
    expect(find.text('ZZWidgetTool'), findsOneWidget);
    expect(find.textContaining('hide tool call'), findsOneWidget);
  });

  testWidgets('each consecutive tool group gets its own hint', (tester) async {
    await _pump(tester, collapse: true, chunk: _twoGroups());
    expect(find.text('ZZAlpha'), findsNothing);
    expect(find.text('ZZBeta'), findsNothing);
    expect(find.text('ZZGamma'), findsNothing);
    expect(find.textContaining('1 tool call'), findsOneWidget);
    expect(find.textContaining('2 tool calls'), findsOneWidget);
  });

  testWidgets('revealing one group leaves the other collapsed', (tester) async {
    await _pump(tester, collapse: true, chunk: _twoGroups());
    await tester.tap(find.textContaining('1 tool call'));
    await tester.pumpAndSettle();
    expect(find.text('ZZAlpha'), findsOneWidget);
    expect(find.text('ZZBeta'), findsNothing);
    expect(find.text('ZZGamma'), findsNothing);
    expect(find.textContaining('hide tool call'), findsOneWidget);
    expect(find.textContaining('2 tool calls'), findsOneWidget);
  });

  testWidgets('a blank delta between tools keeps them one group',
      (tester) async {
    await _pump(tester, collapse: true, chunk: _blankBetween());
    expect(find.textContaining('2 tool calls'), findsOneWidget);
    expect(find.textContaining('1 tool call'), findsNothing);
  });
}
