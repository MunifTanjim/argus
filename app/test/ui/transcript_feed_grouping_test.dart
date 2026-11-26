import 'package:argus/models/entry.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/appearance.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/ui/theme.dart';
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

    test('a summary carries the run entries', () {
      final rows = groupEntries(_entries, verbose: false);
      final runs = rows.whereType<RunSummaryFeedRow>().toList();
      expect(
        [for (final r in runs) r.entries.map((e) => e.id).join()],
        ['hac', 'e', 'f', 'k'],
      );
    });

    test('verbose expands every run into header, entries, footer', () {
      final rows = groupEntries(_entries, verbose: true);
      expect(_shapes(rows), [
        'u', 'Th', 'h', 'a', 'c', 'Bh', 'd', 'Te', 'e', 'Be', 't', //
        'Tf', 'f', 'Bf', 'n', 'Tk', 'k', 'Bk',
      ]);
    });

    test('edge rows carry the run entries', () {
      final rows = groupEntries(_entries, verbose: true);
      final top = rows.whereType<RunEdgeFeedRow>().first;
      expect((top.key, top.top, top.entries.map((e) => e.id).join()),
          ('h', true, 'hac'));
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
      expect((last.key, last.entries.map((e) => e.id).join()), ('k', 'kz'));
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
      expect((rows.single as RunSummaryFeedRow).entries, hasLength(3));
    });

    group('live', () {
      const tail = [
        Entry(id: 'u', kind: EntryKind.user, text: 'go'),
        Entry(id: 'h', kind: EntryKind.thinking, text: 'hmm'),
        Entry(id: 'a', kind: EntryKind.tool, toolName: 'ZZAlpha'),
        Entry(id: 'b', kind: EntryKind.tool, toolName: 'ZZBeta'),
      ];

      test('splits the newest tool out of the last run', () {
        final rows = groupEntries(tail, verbose: false, live: true);
        expect(_shapes(rows), ['u', 'Sh', 'b']);
        expect((rows[1] as RunSummaryFeedRow).entries.map((e) => e.id).join(),
            'ha');
      });

      test('leaves earlier runs alone', () {
        expect(_shapes(groupEntries(_entries, verbose: false, live: true)),
            ['u', 'Sh', 'd', 'Se', 't', 'Sf', 'n', 'Sk']);
      });

      test('a run that ends in thinking splits nothing', () {
        final rows = groupEntries([
          ...tail,
          const Entry(id: 'k', kind: EntryKind.thinking, text: 'more'),
        ], verbose: false, live: true);
        expect(_shapes(rows), ['u', 'Sh']);
      });

      test('a one-tool run shows only the tool', () {
        final rows = groupEntries(const [
          Entry(id: 'u', kind: EntryKind.user, text: 'go'),
          Entry(id: 'a', kind: EntryKind.tool, toolName: 'ZZAlpha'),
        ], verbose: false, live: true);
        expect(_shapes(rows), ['u', 'a']);
      });

      test('an expanded run is not split', () {
        expect(_shapes(groupEntries(tail, verbose: true, live: true)),
            ['u', 'Th', 'h', 'a', 'b', 'Bh']);
      });
    });
  });

  Finder inKey(String key, Finder f) =>
      find.descendant(of: find.byKey(ValueKey(key)), matching: f);

  // Each icon is dim and has its own count next to it.
  void expectIcons(
      WidgetTester tester, String key, List<(IconData, int)> icons) {
    for (final (icon, n) in icons) {
      final f = inKey(key, find.byIcon(icon));
      expect(f, findsOneWidget, reason: '$icon');
      expect(tester.widget<Icon>(f).color, ArgusTokens.fallback.textDim);
      final pair = find.ancestor(of: f, matching: find.byType(Row)).first;
      expect(find.descendant(of: pair, matching: find.text('$n')),
          findsOneWidget, reason: '$icon count');
    }
  }

  testWidgets('default: runs collapsed to icons, words for readers', (
    tester,
  ) async {
    final sem = tester.ensureSemantics();
    await tester.pumpWidget(_feed({}));
    await tester.pumpAndSettle();
    expectIcons(tester, 'run:h',
        [(Icons.lightbulb_outline, 1), (Icons.play_arrow, 2)]);
    expect(inKey('run:h', find.byIcon(Icons.chevron_right)), findsOneWidget);
    expect(find.bySemanticsLabel('thought, used 2 other tools'),
        findsOneWidget);
    expect(find.byKey(const ValueKey('run:f')), findsOneWidget);
    expectIcons(tester, 'run:f', [(Icons.play_arrow, 1)]);
    expectIcons(tester, 'run:e', [(Icons.school_outlined, 1)]);
    expectIcons(tester, 'run:k', [(Icons.lightbulb_outline, 1)]);
    expect(find.text('used ZZGamma'), findsNothing); // words only for readers
    expect(find.text('ZZAlpha'), findsNothing);
    expect(find.text('Thinking'), findsNothing);
    expect(find.text('prose'), findsOneWidget);
    sem.dispose();
  });

  testWidgets('tap expands; footer and header collapse', (tester) async {
    final sem = tester.ensureSemantics();
    await tester.pumpWidget(_feed({}));
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const ValueKey('run:h')));
    await tester.pump();
    expect(find.byKey(const ValueKey('run-top:h')), findsOneWidget);
    expect(inKey('run-top:h', find.byIcon(Icons.expand_more)), findsOneWidget);
    expectIcons(tester, 'run-top:h', [(Icons.lightbulb_outline, 1), (Icons.play_arrow, 2)]);
    expect(
      find.bySemanticsLabel('collapse, thought, used 2 other tools'),
      findsNWidgets(2), // header and footer read the same
    );
    expect(find.text('ZZAlpha'), findsOneWidget);
    expect(find.text('ZZBeta'), findsOneWidget);
    expect(find.text('Thinking'), findsOneWidget);
    expect(find.byKey(const ValueKey('run-bottom:h')), findsOneWidget);
    expect(
      inKey('run-bottom:h', find.byIcon(Icons.expand_less)),
      findsOneWidget,
    );
    expectIcons(tester, 'run-bottom:h', [(Icons.lightbulb_outline, 1), (Icons.play_arrow, 2)]);
    expect(find.textContaining('collapse'), findsNothing);

    await tester.tap(find.byKey(const ValueKey('run-bottom:h')));
    await tester.pump();
    expect(find.text('ZZAlpha'), findsNothing);
    expect(find.byKey(const ValueKey('run:h')), findsOneWidget);

    await tester.tap(find.byKey(const ValueKey('run:h')));
    await tester.pump();
    await tester.tap(find.byKey(const ValueKey('run-top:h')));
    await tester.pump();
    expect(find.text('ZZAlpha'), findsNothing);
    expect(find.byKey(const ValueKey('run-bottom:h')), findsNothing);
    sem.dispose();
  });

  testWidgets('verbose: runs start expanded and can collapse', (tester) async {
    await tester.pumpWidget(_feed({'appearance.verboseTranscript': 'true'}));
    await tester.pumpAndSettle();
    expect(find.text('ZZAlpha'), findsOneWidget);
    expect(find.text('ZZGamma'), findsOneWidget);
    expectIcons(tester, 'run-top:h', [(Icons.lightbulb_outline, 1), (Icons.play_arrow, 2)]);
    for (final k in ['h', 'e', 'f', 'k']) {
      expect(find.byKey(ValueKey('run-bottom:$k')), findsOneWidget);
    }

    await tester.tap(find.byKey(const ValueKey('run-top:h')));
    await tester.pump();
    expect(find.text('ZZAlpha'), findsNothing);
    expect(find.byKey(const ValueKey('run:h')), findsOneWidget);
    expect(find.text('ZZGamma'), findsOneWidget);
  });

  testWidgets('expanding a run near the bottom brings it into view', (
    tester,
  ) async {
    final long = [
      for (var i = 0; i < 30; i++)
        Entry(id: 'p$i', kind: EntryKind.text, text: 'prose $i'),
      for (var i = 0; i < 6; i++)
        Entry(id: 't$i', kind: EntryKind.tool, toolName: 'ZZTool$i'),
      for (var i = 30; i < 60; i++)
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
              entries: long,
              stickToBottom: false,
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    final lc = tester
        .widget<SuperSliverList>(find.byType(SuperSliverList))
        .listController!;
    final sc = tester
        .widget<CustomScrollView>(find.byType(CustomScrollView))
        .controller!;
    // Put the collapsed run's summary (row 30) at the bottom edge.
    lc.jumpToItem(
      index: 30,
      scrollController: sc,
      alignment: 1,
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('run:t0')));
    await tester.pumpAndSettle();
    final screen = tester.getRect(find.byType(CustomScrollView));
    final bottom = tester.getRect(find.byKey(const ValueKey('run-bottom:t0')));
    final top = tester.getRect(find.byKey(const ValueKey('run-top:t0')));
    expect(
      bottom.bottom,
      lessThanOrEqualTo(screen.bottom + 1),
      reason: 'the run\'s bottom row is on screen',
    );
    expect(
      top.top,
      greaterThanOrEqualTo(screen.top - 1),
      reason: 'and so is its top row',
    );
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

    final lc = tester
        .widget<SuperSliverList>(find.byType(SuperSliverList))
        .listController!;
    final sc = tester
        .widget<CustomScrollView>(find.byType(CustomScrollView))
        .controller!;
    lc.jumpToItem(
      index: entries.length - 1,
      scrollController: sc,
      alignment: 1,
    );
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('u')), findsNothing, reason: 'unmounted');

    lc.jumpToItem(
      index: 0,
      scrollController: sc,
      alignment: 0,
    );
    await tester.pumpAndSettle();
    expect(find.text('Show less'), findsOneWidget);
  });

  testWidgets('a failed tool adds a dim failure icon and count', (
    tester,
  ) async {
    await tester.pumpWidget(const ProviderScope(
      child: MaterialApp(
        home: Scaffold(
          body: TranscriptFeed(
            detailRef: ToolDetailRef.live('s'),
            stickToBottom: false,
            entries: [
              Entry(
                  id: 'a',
                  kind: EntryKind.tool,
                  toolName: 'ZZAlpha',
                  resultIsError: true),
            ],
          ),
        ),
      ),
    ));
    await tester.pumpAndSettle();
    expectIcons(tester, 'run:a',
        [(Icons.play_arrow, 1), (Icons.error_outline, 1)]);
    expect(
      tester.getTopLeft(inKey('run:a', find.byIcon(Icons.error_outline))).dx,
      greaterThan(
          tester.getTopLeft(inKey('run:a', find.byIcon(Icons.play_arrow))).dx),
    );
  });

  testWidgets('a run with every kind fits a narrow screen', (tester) async {
    tester.view.physicalSize = const Size(320, 600);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(const ProviderScope(
      child: MaterialApp(
        home: Scaffold(
          body: TranscriptFeed(
            detailRef: ToolDetailRef.live('s', agent: 'claude'),
            stickToBottom: false,
            entries: [
              Entry(id: 'h', kind: EntryKind.thinking, text: 'hmm'),
              Entry(id: 'r', kind: EntryKind.tool, toolName: 'Read'),
              Entry(id: 'g', kind: EntryKind.tool, toolName: 'Grep'),
              Entry(id: 'e', kind: EntryKind.tool, toolName: 'Edit'),
              Entry(id: 'b', kind: EntryKind.tool, toolName: 'Bash'),
              Entry(id: 'w', kind: EntryKind.tool, toolName: 'WebFetch'),
              Entry(id: 't', kind: EntryKind.tool, toolName: 'TodoWrite'),
              Entry(id: 's', kind: EntryKind.skill, toolName: 'Skill'),
              Entry(id: 'x', kind: EntryKind.tool, toolName: 'ZZOther'),
              Entry(
                  id: 'f',
                  kind: EntryKind.tool,
                  toolName: 'Bash',
                  resultIsError: true),
            ],
          ),
        ),
      ),
    ));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(inKey('run:h', find.byIcon(Icons.error_outline)).hitTestable(),
        findsOneWidget);
  });

  testWidgets('a live feed shows the newest tool as a row', (tester) async {
    await tester.pumpWidget(const ProviderScope(
      child: MaterialApp(
        home: Scaffold(
          body: TranscriptFeed(
            detailRef: ToolDetailRef.live('s'),
            stickToBottom: false,
            live: true,
            entries: [
              Entry(id: 'a', kind: EntryKind.tool, toolName: 'ZZAlpha'),
              Entry(id: 'b', kind: EntryKind.tool, toolName: 'ZZBeta'),
            ],
          ),
        ),
      ),
    ));
    await tester.pumpAndSettle();
    expect(find.text('ZZBeta'), findsOneWidget);
    expectIcons(tester, 'run:a', [(Icons.play_arrow, 1)]);
  });
}
