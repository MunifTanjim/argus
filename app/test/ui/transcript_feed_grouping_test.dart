import 'package:argus/models/entry.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/appearance.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/ui/transcript_feed.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:super_sliver_list/super_sliver_list.dart';

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

// Tool names are absent from the tool registry so ItemRow renders the raw name.
const _entries = [
  Entry(id: 'u', kind: EntryKind.user, text: 'go'),
  Entry(id: 'a', kind: EntryKind.tool, toolName: 'ZZAlpha'),
  Entry(id: 'b', kind: EntryKind.text, text: '  '),
  Entry(id: 'c', kind: EntryKind.tool, toolName: 'ZZBeta'),
  Entry(id: 'd', kind: EntryKind.text, text: 'prose'),
  Entry(id: 'e', kind: EntryKind.skill, toolName: 'Skill', inputPreview: 'x'),
  Entry(
    id: 't',
    kind: EntryKind.subagent,
    text: 'hi',
    subagents: [Subagent(name: 'mate', isTeammate: true)],
  ),
  Entry(id: 'f', kind: EntryKind.tool, toolName: 'ZZGamma'),
];

void main() {
  test('collapse off keeps every entry', () {
    final rows = groupEntries(_entries, collapseTools: false);
    expect(rows.length, _entries.length);
    expect(rows.every((r) => r is EntryFeedRow), isTrue);
  });

  test(
    'collapse on groups runs; blank text is transparent; teammate splits',
    () {
      final rows = groupEntries(_entries, collapseTools: true);
      final shape = rows
          .map(
            (r) => switch (r) {
              EntryFeedRow(:final entry) => entry.id,
              ToolGroupFeedRow(:final tools) =>
                'G(${tools.map((t) => t.id).join()})',
            },
          )
          .toList();
      expect(shape, ['u', 'G(ac)', 'd', 'G(e)', 't', 'G(f)']);
    },
  );

  testWidgets('a group expands in place', (tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          appearanceStoreProvider.overrideWithValue(
            AppearanceStore(_FakeKv({'appearance.collapseToolCalls': 'true'})),
          ),
        ],
        child: const MaterialApp(
          home: Scaffold(
            body: TranscriptFeed(
              detailRef: ToolDetailRef.live('s'),
              entries: _entries,
              stickToBottom: false,
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('ZZAlpha'), findsNothing);
    expect(find.text('▸ 2 tool calls'), findsOneWidget);
    await tester.tap(find.text('▸ 2 tool calls'));
    await tester.pump();
    expect(find.text('ZZAlpha'), findsOneWidget);
    expect(find.text('ZZBeta'), findsOneWidget);
  });

  testWidgets('an expanded entry stays expanded after scrolling away', (
    tester,
  ) async {
    final prompt = List.generate(30, (i) => 'line $i').join('\n');
    final entries = [
      Entry(id: 'u', kind: EntryKind.user, text: prompt),
      for (var i = 0; i < 100; i++)
        Entry(id: 'p$i', kind: EntryKind.text, text: 'prose $i'),
    ];
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          appearanceStoreProvider.overrideWithValue(
            AppearanceStore(_FakeKv({})),
          ),
        ],
        child: MaterialApp(
          home: Scaffold(
            body: TranscriptFeed(
              detailRef: const ToolDetailRef.live('s'),
              entries: entries,
              stickToBottom: false,
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Show more'));
    await tester.pumpAndSettle();
    expect(find.text('Show less'), findsOneWidget);

    final list = tester.widget<SuperListView>(find.byType(SuperListView));
    list.listController!.jumpToItem(
      index: entries.length - 1,
      scrollController: list.controller!,
      alignment: 1,
    );
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('u')), findsNothing, reason: 'unmounted');

    list.listController!.jumpToItem(
      index: 0,
      scrollController: list.controller!,
      alignment: 0,
    );
    await tester.pumpAndSettle();
    expect(find.text('Show less'), findsOneWidget);
  });
}
