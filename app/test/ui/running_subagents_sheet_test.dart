import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/ui/running_subagents_sheet.dart';
import 'package:argus/ui/subagent_trace_screen.dart';

Session _s(List<Map<String, dynamic>> subagents) => Session.fromJson({
  'id': 'n:%1',
  'agent': 'claude',
  'status': 'working',
  'source': 'hooked',
  'tmux': {'pane_id': '%1'},
  'subagents': subagents,
});

List<Map<String, dynamic>> _two() => [
  {
    'id': 'a1',
    'type': 'Explore',
    'desc': 'find it',
    'since': DateTime.now()
        .toUtc()
        .subtract(const Duration(seconds: 75))
        .toIso8601String(),
  },
  {'id': 'a2', 'name': 'scout', 'type': 'Plan', 'desc': 'map it'},
];

Future<ProviderContainer> _open(WidgetTester t) async {
  final container = ProviderContainer();
  addTearDown(container.dispose);
  container.read(sessionsProvider.notifier).put(_s(_two()));
  await t.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: MaterialApp(
        home: Builder(
          builder: (context) => TextButton(
            onPressed: () => showRunningSubagents(
              context,
              sessionId: 'n:%1',
              agent: 'claude',
            ),
            child: const Text('open'),
          ),
        ),
      ),
    ),
  );
  await t.tap(find.text('open'));
  await t.pumpAndSettle();
  return container;
}

class _Pushes extends NavigatorObserver {
  final routes = <Route<dynamic>>[];
  @override
  void didPush(Route<dynamic> route, Route<dynamic>? previous) =>
      routes.add(route);
}

void main() {
  testWidgets('a picked subagent opens on the caller\'s navigator', (t) async {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    container.read(sessionsProvider.notifier).put(_s(_two()));
    final root = _Pushes();
    final shell = _Pushes();
    await t.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: MaterialApp(
          navigatorObservers: [root],
          home: Navigator(
            observers: [shell],
            onGenerateRoute: (_) => MaterialPageRoute<void>(
              builder: (context) => TextButton(
                onPressed: () => showRunningSubagents(
                  context,
                  sessionId: 'n:%1',
                  agent: 'claude',
                ),
                child: const Text('open'),
              ),
            ),
          ),
        ),
      ),
    );
    await t.tap(find.text('open'));
    await t.pumpAndSettle();
    root.routes.clear();
    shell.routes.clear();
    await t.tap(find.text('scout'));
    await t.pumpAndSettle();
    expect(find.byType(SubagentTraceScreen), findsOneWidget);
    expect(shell.routes, hasLength(1));
    expect(root.routes, isEmpty);
    expect(
      find.descendant(
        of: find.byType(Navigator).last,
        matching: find.byType(SubagentTraceScreen),
      ),
      findsOneWidget,
    );
  });

  testWidgets('a nameless, typeless subagent reads "Subagent"', (t) async {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    container
        .read(sessionsProvider.notifier)
        .put(
          _s([
            {'id': 'a9'},
          ]),
        );
    await t.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: MaterialApp(
          home: Builder(
            builder: (context) => TextButton(
              onPressed: () => showRunningSubagents(
                context,
                sessionId: 'n:%1',
                agent: 'claude',
              ),
              child: const Text('open'),
            ),
          ),
        ),
      ),
    );
    await t.tap(find.text('open'));
    await t.pumpAndSettle();
    expect(find.text('Subagent'), findsOneWidget);
    expect(find.text('a9'), findsNothing);
  });

  testWidgets('lists name-or-type, desc and elapsed', (t) async {
    await _open(t);
    expect(find.text('Explore'), findsOneWidget);
    expect(find.text('scout'), findsOneWidget);
    expect(find.text('Plan'), findsNothing);
    expect(find.text('find it'), findsOneWidget);
    expect(find.text('map it'), findsOneWidget);
    expect(find.textContaining(RegExp(r'^1m \d\ds$')), findsOneWidget);
  });

  testWidgets('tapping a row opens its live trace', (t) async {
    await _open(t);
    await t.tap(find.text('scout'));
    await t.pumpAndSettle();
    final screen = t.widget<SubagentTraceScreen>(
      find.byType(SubagentTraceScreen),
    );
    expect(screen.item.soleSubagent!.id, 'a2');
    expect(screen.parentRef.sessionId, 'n:%1');
    expect(screen.parentRef.agent, 'claude');
  });

  testWidgets('closes when the last subagent finishes', (t) async {
    final c = await _open(t);
    c.read(sessionsProvider.notifier).put(_s([]));
    await t.pumpAndSettle();
    expect(find.text('Explore'), findsNothing);
    expect(find.byType(BottomSheet), findsNothing);
  });

  testWidgets('a closing sheet leaves the trace it opened', (t) async {
    final c = await _open(t);
    await t.tap(find.text('scout'));
    await t.pump();
    c.read(sessionsProvider.notifier).put(_s([]));
    await t.pumpAndSettle();
    expect(find.byType(SubagentTraceScreen), findsOneWidget);
  });
}
