import 'package:argus/models/entry.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/ui/entry_row.dart';
import 'package:argus/ui/item_detail_screen.dart';
import 'package:argus/ui/subagent_trace_screen.dart';
import 'package:argus/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

Future<void> _pump(WidgetTester tester, Entry e, {bool scroll = false}) {
  var expanded = false;
  return tester.pumpWidget(
    ProviderScope(
      overrides: [gatewayProvider.overrideWithValue(null)],
      child: MaterialApp(
        home: Scaffold(
          body: StatefulBuilder(
            builder: (context, setState) {
              final row = EntryRow(
                detailRef: const ToolDetailRef.live('s'),
                entry: e,
                expanded: expanded,
                onToggle: () => setState(() => expanded = !expanded),
              );
              // Scrollable parent so tall expanded content doesn't overflow.
              return scroll ? SingleChildScrollView(child: row) : row;
            },
          ),
        ),
      ),
    ),
  );
}

TextSpan _span(WidgetTester tester, String text) {
  final rich = tester.widget<Text>(find.textContaining(text));
  TextSpan? hit;
  rich.textSpan!.visitChildren((s) {
    if (s is TextSpan && s.text == text) {
      hit = s;
      return false;
    }
    return true;
  });
  return hit!;
}

void main() {
  testWidgets('user entry renders as a full-width band', (tester) async {
    await _pump(
      tester,
      const Entry(id: 'u', kind: EntryKind.user, text: 'fix the bug'),
    );
    expect(find.text('You'), findsOneWidget);
    expect(find.textContaining('fix the bug'), findsOneWidget);
    final band = tester.getSize(find.byKey(const ValueKey('user-band')));
    expect(band.width, tester.getSize(find.byType(Scaffold)).width);
  });

  testWidgets('text entry renders markdown', (tester) async {
    await _pump(
      tester,
      const Entry(id: 't', kind: EntryKind.text, text: 'all **done**'),
    );
    expect(find.textContaining('all'), findsWidgets);
  });

  testWidgets('thinking expands inline on tap', (tester) async {
    await _pump(
      tester,
      const Entry(id: 'th', kind: EntryKind.thinking, text: 'deep reasoning'),
    );
    expect(find.text('deep reasoning'), findsNothing);
    await tester.tap(find.text('Thinking'));
    await tester.pump();
    expect(find.text('deep reasoning'), findsOneWidget);
  });

  testWidgets('turn_end shows the same stats as the TUI', (tester) async {
    await _pump(
      tester,
      const Entry(
        id: 'e',
        kind: EntryKind.turnEnd,
        modelName: 'Opus 4.8',
        thinking: 2,
        toolCount: 5,
        usage: Usage(input: 900, output: 12300, cacheRead: 80000),
        hasContext: true,
        contextFirstPct: 40,
        contextPct: 45,
        contextDeltaTokens: 3100,
        durationMs: 62000,
        timestamp: '2026-10-08T14:32:05Z',
      ),
    );
    final text = tester
        .widget<Text>(find.textContaining('Opus 4.8'))
        .textSpan!
        .toPlainText();
    // Output tokens, not input + cache: the turn's spend, as the TUI shows.
    expect(text, contains('12.3k'));
    expect(text, isNot(contains('93.2k')));
    expect(text, contains('2'));
    expect(text, contains('5'));
    expect(text, contains('ctx 40% → 45% (+3.1k)'));
    expect(text, contains('1m 2s'));
    final local = DateTime.parse('2026-10-08T14:32:05Z').toLocal();
    String p(int n) => n.toString().padLeft(2, '0');
    expect(
      text,
      contains('${p(local.hour)}:${p(local.minute)}:${p(local.second)}'),
    );
  });

  testWidgets('a full turn_end wraps on a phone instead of overflowing', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(360 * 3, 800 * 3);
    tester.view.devicePixelRatio = 3;
    addTearDown(tester.view.reset);
    await _pump(
      tester,
      const Entry(
        id: 'e',
        kind: EntryKind.turnEnd,
        interrupted: true,
        modelName: 'Opus 4.8',
        thinking: 12,
        toolCount: 48,
        usage: Usage(output: 123400),
        hasContext: true,
        contextFirstPct: 40,
        contextPct: 85,
        contextDeltaTokens: 93100,
        durationMs: 3723000,
        timestamp: '2026-10-08T14:32:05Z',
      ),
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('interrupted turn_end says so', (tester) async {
    await _pump(
      tester,
      const Entry(id: 'e', kind: EntryKind.turnEnd, interrupted: true),
    );
    expect(find.textContaining('interrupted'), findsOneWidget);
  });

  testWidgets('tool entry drills into the detail screen', (tester) async {
    await _pump(
      tester,
      const Entry(
        id: 'x',
        kind: EntryKind.tool,
        toolName: 'Bash',
        inputPreview: 'ls',
        toolInput: '{"command":"ls"}',
        result: 'ok',
      ),
    );
    await tester.tap(find.text('Bash'));
    await tester.pumpAndSettle();
    expect(find.byType(ItemDetailScreen), findsOneWidget);
  });

  testWidgets('long user prompt folds behind Show more / Show less', (
    tester,
  ) async {
    final long = List.generate(30, (i) => 'line $i').join('\n');
    await _pump(
      tester,
      Entry(id: 'u', kind: EntryKind.user, text: long),
      scroll: true,
    );
    expect(find.text('Show more'), findsOneWidget);
    expect(find.textContaining('line 29'), findsNothing);
    await tester.tap(find.text('Show more'));
    await tester.pump();
    expect(find.text('Show less'), findsOneWidget);
    expect(find.textContaining('line 29'), findsOneWidget);
    await tester.ensureVisible(find.text('Show less'));
    await tester.tap(find.text('Show less'));
    await tester.pump();
    expect(find.text('Show more'), findsOneWidget);
    expect(find.textContaining('line 29'), findsNothing);
  });

  testWidgets('shell entry shows the command, output on expand', (
    tester,
  ) async {
    await _pump(
      tester,
      const Entry(
        id: 'sh',
        kind: EntryKind.shell,
        text: 'git status',
        detail: 'On branch main\nnothing to commit',
      ),
    );
    expect(find.text('Shell'), findsOneWidget);
    expect(find.text(r'$ git status'), findsOneWidget);
    expect(find.textContaining('nothing to commit'), findsNothing);
    await tester.tap(find.text('Shell'));
    await tester.pump();
    expect(find.textContaining('nothing to commit'), findsOneWidget);
  });

  testWidgets('system entry shows its label preview', (tester) async {
    await _pump(
      tester,
      const Entry(id: 's', kind: EntryKind.system, label: 'Recap'),
    );
    expect(find.text('System'), findsOneWidget);
    expect(find.textContaining('Recap'), findsOneWidget);
  });

  testWidgets('thinking without text is not tappable', (tester) async {
    await _pump(
      tester,
      const Entry(id: 'th', kind: EntryKind.thinking, text: '  '),
    );
    expect(find.text('Thinking'), findsOneWidget);
    expect(find.byType(InkWell), findsNothing);
    await tester.tap(find.text('Thinking'));
    await tester.pump();
    expect(find.byType(InkWell), findsNothing);
  });

  testWidgets('active teammate message renders its full markdown body', (
    tester,
  ) async {
    await _pump(
      tester,
      const Entry(
        id: 'tm',
        kind: EntryKind.subagent,
        text: 'first paragraph\n\nsecond **paragraph**',
        subagents: [Subagent(name: 'alice', isTeammate: true)],
      ),
    );
    expect(find.text('alice'), findsOneWidget);
    expect(find.textContaining('first paragraph'), findsOneWidget);
    expect(find.textContaining('second'), findsOneWidget);
    expect(find.textContaining('is done'), findsNothing);
  });

  testWidgets('idle teammate renders as an ItemRow saying is done', (
    tester,
  ) async {
    await _pump(
      tester,
      const Entry(
        id: 'tm',
        kind: EntryKind.subagent,
        text: 'ignored body',
        subagents: [Subagent(name: 'alice', isTeammate: true, idle: true)],
      ),
    );
    expect(find.text('alice'), findsOneWidget);
    expect(find.textContaining('is done'), findsOneWidget);
    expect(find.textContaining('ignored body'), findsNothing);
  });

  testWidgets('subagent with a trace drills into SubagentTraceScreen', (
    tester,
  ) async {
    await _pump(
      tester,
      const Entry(
        id: 'sa',
        kind: EntryKind.subagent,
        subagents: [
          Subagent(
            id: 'a1',
            name: 'scout',
            type: 'Explore',
            desc: 'map the auth flow',
            hasTrace: true,
            trace: [Entry(id: 't0', kind: EntryKind.text, text: 'found it')],
          ),
        ],
      ),
    );
    await tester.tap(find.text('scout (Explore)'));
    await tester.pumpAndSettle();
    expect(find.byType(SubagentTraceScreen), findsOneWidget);
    expect(find.textContaining('found it'), findsOneWidget);
  });

  testWidgets('long item label truncates instead of overflowing', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(400, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final long = 'x' * 80;
    for (final desc in ['map the auth flow', '']) {
      await _pump(
        tester,
        Entry(
          id: 'sa',
          kind: EntryKind.subagent,
          subagents: [Subagent(id: 'a1', name: long, type: 'Explore', desc: desc)],
        ),
      );
      expect(tester.takeException(), isNull);
      if (desc.isNotEmpty) expect(find.text(desc), findsOneWidget);
    }
  });

  testWidgets('turn_end shows context % colored by pressure', (tester) async {
    await _pump(
      tester,
      const Entry(
        id: 'e1',
        kind: EntryKind.turnEnd,
        hasContext: true,
        contextFirstPct: 85,
        contextPct: 85,
      ),
    );
    expect(_span(tester, 'ctx 85%').style?.color, const Color(0xFFfb4934));
  });

  testWidgets('turn_end healthy context % stays dim', (tester) async {
    await _pump(
      tester,
      const Entry(
        id: 'e2',
        kind: EntryKind.turnEnd,
        hasContext: true,
        contextFirstPct: 42,
        contextPct: 42,
      ),
    );
    expect(_span(tester, 'ctx 42%').style?.color, AppColors.dim);
  });

  testWidgets('compact divider renders its summary', (tester) async {
    await _pump(
      tester,
      const Entry(id: 'c', kind: EntryKind.compact, summary: 'freed 40k'),
    );
    expect(find.text('freed 40k'), findsOneWidget);
  });

  testWidgets('compact divider falls back to Context compressed', (
    tester,
  ) async {
    await _pump(tester, const Entry(id: 'c', kind: EntryKind.compact));
    expect(find.text('Context compressed'), findsOneWidget);
  });
}
