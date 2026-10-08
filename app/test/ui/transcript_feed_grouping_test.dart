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
  Entry(id: 'h', kind: EntryKind.thinking, text: 'hmm'),
  Entry(id: 'a', kind: EntryKind.tool, toolName: 'ZZAlpha'),
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
  Entry(id: 'n', kind: EntryKind.turnEnd),
  Entry(id: 'k', kind: EntryKind.thinking, text: 'more'),
];

String _shape(FeedRow r) => switch (r) {
  EntryFeedRow(:final entry) => entry.id,
  RunSummaryFeedRow(:final key) => 'S$key',
  RunEdgeFeedRow(:final key, :final top) => '${top ? 'T' : 'B'}$key',
};

List<String> _shapes(List<FeedRow> rows) => rows.map(_shape).toList();

Widget _feed(Map<String, String> kv) => ProviderScope(
  overrides: [
    appearanceStoreProvider.overrideWithValue(AppearanceStore(_FakeKv(kv))),
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
);

void main() {
  group('groupEntries', () {
    test('collapsed by default: one summary per run', () {
      final rows = groupEntries(_entries, verbose: false);
      expect(_shapes(rows), [
        'u',
        'Sh',
        'd',
        'Se',
        't',
        'Sf',
        'n',
        'Sk',
      ]);
    });

    test('summary counts thinking and tools separately', () {
      final rows = groupEntries(_entries, verbose: false);
      final runs = rows.whereType<RunSummaryFeedRow>().toList();
      expect(
        [for (final r in runs) (r.thinking, r.tools)],
        [(1, 2), (0, 1), (0, 1), (1, 0)],
      );
    });

    test('verbose expands every run into header, entries, footer', () {
      final rows = groupEntries(_entries, verbose: true);
      expect(_shapes(rows), [
        'u', 'Th', 'h', 'a', 'c', 'Bh', 'd', 'Te', 'e', 'Be', 't', //
        'Tf', 'f', 'Bf', 'n', 'Tk', 'k', 'Bk',
      ]);
    });

    test('edge rows carry the run counts', () {
      final rows = groupEntries(_entries, verbose: true);
      final top = rows.whereType<RunEdgeFeedRow>().first;
      expect((top.key, top.top, top.thinking, top.tools), ('h', true, 1, 2));
    });

    test('overrides win over the verbose default', () {
      expect(
        _shapes(groupEntries(_entries, verbose: false, overrides: {'h': true})),
        [
          'u',
          'Th',
          'h',
          'a',
          'c',
          'Bh',
          'd',
          'Se',
          't',
          'Sf',
          'n',
          'Sk',
        ],
      );
      expect(
        _shapes(
          groupEntries(
            _entries,
            verbose: true,
            overrides: {'h': false, 'k': false},
          ),
        ),
        [
          'u',
          'Sh',
          'd',
          'Te',
          'e',
          'Be',
          't',
          'Tf',
          'f',
          'Bf',
          'n',
          'Sk',
        ],
      );
    });

    test('run key survives growth', () {
      final grown = [
        ..._entries,
        const Entry(id: 'z', kind: EntryKind.tool, toolName: 'ZZDelta'),
      ];
      final last =
          groupEntries(grown, verbose: false).last as RunSummaryFeedRow;
      expect((last.key, last.thinking, last.tools), ('k', 1, 1));
    });

    test('a subagent spawn ends a run and stands alone', () {
      const entries = [
        Entry(id: 'h', kind: EntryKind.thinking, text: 'hmm'),
        Entry(id: 'a', kind: EntryKind.tool, toolName: 'ZZAlpha'),
        Entry(
          id: 's',
          kind: EntryKind.subagent,
          toolName: 'Task',
          subagents: [Subagent(name: 'worker')],
        ),
        Entry(id: 'c', kind: EntryKind.tool, toolName: 'ZZBeta'),
      ];
      expect(_shapes(groupEntries(entries, verbose: false)), [
        'Sh',
        's',
        'Sc',
      ]);
    });

    test('agent-ref ops stay inside a run', () {
      const entries = [
        Entry(id: 'a', kind: EntryKind.tool, toolName: 'ZZAlpha'),
        Entry(id: 'w', kind: EntryKind.subagent, toolName: 'wait_agent'),
        Entry(id: 'x', kind: EntryKind.subagent, toolName: 'close_agent'),
      ];
      final rows = groupEntries(entries, verbose: false);
      expect(_shapes(rows), ['Sa']);
      expect((rows.single as RunSummaryFeedRow).tools, 3);
    });
  });

  testWidgets('default: runs collapsed to a count summary', (tester) async {
    await tester.pumpWidget(_feed({}));
    await tester.pumpAndSettle();
    expect(find.text('▸ 1 thinking · 2 tools'), findsOneWidget);
    expect(find.text('▸ 1 tool'), findsNWidgets(2));
    expect(find.text('▸ 1 thinking'), findsOneWidget);
    expect(find.text('ZZAlpha'), findsNothing);
    expect(find.text('Thinking'), findsNothing);
    expect(find.text('prose'), findsOneWidget);
  });

  testWidgets('tap expands; footer and header collapse', (tester) async {
    await tester.pumpWidget(_feed({}));
    await tester.pumpAndSettle();

    await tester.tap(find.text('▸ 1 thinking · 2 tools'));
    await tester.pump();
    expect(find.text('▾ collapse · 1 thinking · 2 tools'), findsOneWidget);
    expect(find.text('ZZAlpha'), findsOneWidget);
    expect(find.text('ZZBeta'), findsOneWidget);
    expect(find.text('Thinking'), findsOneWidget);
    expect(find.text('▴ collapse'), findsOneWidget);

    await tester.tap(find.text('▴ collapse'));
    await tester.pump();
    expect(find.text('ZZAlpha'), findsNothing);
    expect(find.text('▸ 1 thinking · 2 tools'), findsOneWidget);

    await tester.tap(find.text('▸ 1 thinking · 2 tools'));
    await tester.pump();
    await tester.tap(find.text('▾ collapse · 1 thinking · 2 tools'));
    await tester.pump();
    expect(find.text('ZZAlpha'), findsNothing);
    expect(find.text('▴ collapse'), findsNothing);
  });

  testWidgets('verbose: runs start expanded and can collapse', (tester) async {
    await tester.pumpWidget(_feed({'appearance.verboseTranscript': 'true'}));
    await tester.pumpAndSettle();
    expect(find.text('ZZAlpha'), findsOneWidget);
    expect(find.text('ZZGamma'), findsOneWidget);
    expect(find.text('▾ collapse · 1 thinking · 2 tools'), findsOneWidget);
    expect(find.text('▴ collapse'), findsNWidgets(4));

    await tester.tap(find.text('▾ collapse · 1 thinking · 2 tools'));
    await tester.pump();
    expect(find.text('ZZAlpha'), findsNothing);
    expect(find.text('▸ 1 thinking · 2 tools'), findsOneWidget);
    expect(find.text('ZZGamma'), findsOneWidget);
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
