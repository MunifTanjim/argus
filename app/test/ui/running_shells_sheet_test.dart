import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/entry.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/ui/item_detail_screen.dart';
import 'package:argus/ui/running_shells_sheet.dart';

Session _s(List<Map<String, dynamic>> shells) => Session.fromJson({
  'id': 'n:%1',
  'agent': 'claude',
  'status': 'working',
  'source': 'hooked',
  'tmux': {'pane_id': '%1'},
  'shells': shells,
});

List<Map<String, dynamic>> _two() => [
  {
    'id': 'b1',
    'tool_use_id': 't1',
    'command': 'make test',
    'desc': 'run tests',
    'since': DateTime.now()
        .toUtc()
        .subtract(const Duration(seconds: 75))
        .toIso8601String(),
  },
  {'id': 'b2', 'tool_use_id': 't2', 'command': 'npm run dev'},
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
            onPressed: () => showRunningShells(
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

void main() {
  testWidgets('lists desc-or-command, command and elapsed', (t) async {
    await _open(t);
    expect(find.text('run tests'), findsOneWidget);
    expect(find.text('make test'), findsOneWidget);
    expect(find.text('npm run dev'), findsOneWidget);
    expect(find.textContaining(RegExp(r'^1m \d\ds$')), findsOneWidget);
  });

  testWidgets('tapping a row opens its Bash call', (t) async {
    await _open(t);
    await t.tap(find.text('run tests'));
    await t.pump();
    await t.pump(const Duration(milliseconds: 500));
    final screen = t.widget<ItemDetailScreen>(find.byType(ItemDetailScreen));
    expect(screen.item.kind, EntryKind.tool);
    expect(screen.item.toolName, 'Bash');
    expect(screen.item.toolId, 't1');
    expect(screen.detailRef.sessionId, 'n:%1');
    expect(screen.detailRef.agent, 'claude');
  });

  testWidgets('closes when the last shell finishes', (t) async {
    final c = await _open(t);
    c.read(sessionsProvider.notifier).put(_s([]));
    await t.pumpAndSettle();
    expect(find.text('run tests'), findsNothing);
    expect(find.byType(BottomSheet), findsNothing);
  });
}
