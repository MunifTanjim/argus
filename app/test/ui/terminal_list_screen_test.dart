import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/terminal.dart';
import 'package:argus/state/control.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/state/terminals.dart';
import 'package:argus/state/terminals_api.dart';
import 'package:argus/transport/gateway_client.dart';
import 'package:argus/ui/terminal_list_screen.dart';

class _Seeded extends TerminalsNotifier {
  _Seeded(this.seed);
  final TerminalsState seed;
  var loads = 0;
  @override
  TerminalsState build() => seed;
  @override
  Future<void> load(GatewayClient? client) async => loads++;
}

class _FakeApi implements TerminalsApi {
  Object? failure;
  final killed = <String>[];
  final renamed = <(String, String)>[];
  @override
  Future<NodeTerminal> create(String? nodeId, {String? workspaceId}) async =>
      NodeTerminal(id: '${nodeId ?? 'A'}:@9', command: 'zsh');
  @override
  Future<void> kill(String id) async {
    if (failure != null) throw failure!;
    killed.add(id);
  }
  @override
  Future<void> rename(String id, String name) async => renamed.add((id, name));
}

const _terms = [
  NodeTerminal(id: 'A:@1', nodeId: 'A', nodeLabel: 'home', command: 'zsh', cwd: '~'),
  NodeTerminal(id: 'A:@2', nodeId: 'A', nodeLabel: 'home', name: 'build', command: 'make', cwd: '~/src', attached: true),
];

late _Seeded _notifier;

Future<_FakeApi> _pump(WidgetTester tester, List<NodeTerminal> terms,
    {List<NodeRef> nodes = const [], TerminalsState? state}) async {
  final api = _FakeApi();
  _notifier = _Seeded(state ?? TerminalsState(terminals: terms, loaded: true));
  await tester.pumpWidget(ProviderScope(
    overrides: [
      terminalsProvider.overrideWith(() => _notifier),
      terminalsApiProvider.overrideWithValue(api),
      serverInfoProvider.overrideWith((ref) async => ServerInfo(version: '', nodes: nodes)),
    ],
    child: const MaterialApp(home: TerminalListScreen()),
  ));
  await tester.pump();
  return api;
}

void main() {
  testWidgets('lists terminals with title and cwd', (tester) async {
    await _pump(tester, _terms);
    expect(find.text('zsh'), findsOneWidget);
    expect(find.text('build'), findsOneWidget);
    expect(find.text('~/src'), findsOneWidget);
  });

  testWidgets('empty list says so', (tester) async {
    await _pump(tester, const []);
    expect(find.text('No terminals.'), findsOneWidget);
  });

  testWidgets('shows loading before the first load', (tester) async {
    await _pump(tester, const [], state: const TerminalsState());
    expect(find.byKey(const Key('terminals-loading')), findsOneWidget);
    expect(find.text('No terminals.'), findsNothing);
  });

  testWidgets('shows a load error', (tester) async {
    await _pump(tester, const [], state: const TerminalsState(error: 'down'));
    expect(find.text('Could not load terminals: down'), findsOneWidget);
  });

  testWidgets('a failed kill reloads the list', (tester) async {
    final api = await _pump(tester, _terms);
    api.failure = StateError('unknown terminal: @1');
    await tester.longPress(find.text('zsh'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Kill'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'Kill'));
    await tester.pumpAndSettle();
    expect(find.textContaining('kill failed'), findsOneWidget);
    expect(_notifier.loads, 1);
  });

  testWidgets('long-press kill asks, then kills', (tester) async {
    final api = await _pump(tester, _terms);
    await tester.longPress(find.text('zsh'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Kill'));
    await tester.pumpAndSettle();
    expect(find.text('Kill terminal zsh?'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, 'Kill'));
    await tester.pumpAndSettle();
    expect(api.killed, ['A:@1']);
  });

  testWidgets('long-press rename sends the new name', (tester) async {
    final api = await _pump(tester, _terms);
    await tester.longPress(find.text('zsh'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Rename'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), 'logs');
    await tester.pump();
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();
    expect(api.renamed, [('A:@1', 'logs')]);
  });

  testWidgets('names the node when several nodes can run terminals', (tester) async {
    await _pump(
      tester,
      const [NodeTerminal(id: 'A:@1', nodeId: 'A', nodeLabel: 'alpha', command: 'zsh')],
      nodes: const [NodeRef('A', 'alpha'), NodeRef('B', 'beta')],
    );
    await tester.pump();
    expect(find.text('ALPHA'), findsOneWidget);
    expect(find.byIcon(Icons.dns_outlined), findsOneWidget);
    expect(find.byTooltip('New terminal'), findsOneWidget);
  });
}
