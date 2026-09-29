import 'dart:async';

import 'package:argus/state/gateway.dart';
import 'package:argus/state/projects.dart';
import 'package:argus/state/projects_api.dart';
import 'package:argus/ui/setup_log_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

Map<String, dynamic> _tree(String state, {String tail = ''}) => {
  'projects': [
    {
      'id': 'A:p1',
      'name': 'argus',
      'node_id': 'A',
      'scripts': {'setup': 'make deps'},
      'workspaces': [
        {
          'id': 'A:w2',
          'dir': '/src/argus/.wt/reg',
          'branch': 'feat/reg',
          'setup': {
            'state': state,
            'command': 'make deps',
            'output_tail': tail,
          },
        },
      ],
    },
  ],
};

Future<(ProviderContainer, FakeGatewayClient)> _pump(
  WidgetTester tester, {
  required String state,
  String output = '\x1b[32mstep 1\x1b[0m',
  Future<Object?> Function(String m, Object? p)? handler,
}) async {
  final client = FakeGatewayClient(
    handler ??
        (m, p) async => switch (m) {
          'workspace.setupLog' => {'output': output},
          _ => null,
        },
  );
  final c = ProviderContainer(
    overrides: [
      gatewayProvider.overrideWithValue(null),
      projectsApiProvider.overrideWithValue(ProjectsApi(() => client)),
    ],
  );
  addTearDown(c.dispose);
  await c
      .read(projectsProvider.notifier)
      .load(FakeGatewayClient((m, p) async => _tree(state)));
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: c,
      child: const MaterialApp(home: SetupLogScreen(workspaceId: 'A:w2')),
    ),
  );
  await tester.pump();
  return (c, client);
}

int _logCalls(FakeGatewayClient c) =>
    c.calls.where((x) => x.$1 == 'workspace.setupLog').length;

void main() {
  testWidgets('shows the cleaned log and the title', (tester) async {
    await _pump(tester, state: 'failed');
    expect(find.text('Setup log · reg'), findsOneWidget);
    expect(find.textContaining('step 1', findRichText: true), findsWidgets);
    expect(find.textContaining('\x1b', findRichText: true), findsNothing);
  });

  testWidgets('empty output says so', (tester) async {
    await _pump(tester, state: 'ok', output: '');
    expect(find.text('No output.'), findsOneWidget);
  });

  testWidgets('polls while running and stops with one last fetch', (
    tester,
  ) async {
    final (c, client) = await _pump(tester, state: 'running');
    expect(_logCalls(client), 1);
    await tester.pump(const Duration(seconds: 1));
    await tester.pump(const Duration(seconds: 1));
    expect(_logCalls(client), 3);

    await c
        .read(projectsProvider.notifier)
        .load(FakeGatewayClient((m, p) async => _tree('ok')));
    await tester.pump();
    expect(_logCalls(client), 4);
    await tester.pump(const Duration(seconds: 3));
    expect(_logCalls(client), 4);
  });

  testWidgets('does not poll when setup is not running', (tester) async {
    final (_, client) = await _pump(tester, state: 'failed');
    await tester.pump(const Duration(seconds: 3));
    expect(_logCalls(client), 1);
  });

  testWidgets('Rerun calls runSetup and fetches again', (tester) async {
    final (_, client) = await _pump(tester, state: 'failed');
    await tester.tap(find.byTooltip('Rerun setup'));
    await tester.pumpAndSettle();
    final methods = client.calls.map((x) => x.$1).toList();
    expect(methods.indexOf('workspace.runSetup'), 1);
    expect(methods.last, 'workspace.setupLog');
    expect(_logCalls(client), 2);
    expect(find.text('running setup'), findsOneWidget);
  });

  testWidgets('a slow fetch is not overlapped by the next poll', (
    tester,
  ) async {
    final gates = <Completer<Object?>>[];
    final (_, client) = await _pump(
      tester,
      state: 'running',
      handler: (m, p) {
        final g = Completer<Object?>();
        gates.add(g);
        return g.future;
      },
    );
    await tester.pump(const Duration(seconds: 3));
    expect(_logCalls(client), 1);
    gates.single.complete({'output': 'a'});
    await tester.pump();
    await tester.pump(const Duration(seconds: 1));
    expect(_logCalls(client), 2);
    gates.last.complete({'output': 'b'});
    await tester.pump();
  });

  testWidgets('an older reply does not overwrite a newer one', (tester) async {
    final gates = <Completer<Object?>>[];
    await _pump(
      tester,
      state: 'failed',
      handler: (m, p) {
        if (m != 'workspace.setupLog') return Future.value(null);
        final g = Completer<Object?>();
        gates.add(g);
        return g.future;
      },
    );
    await tester.tap(find.byTooltip('Rerun setup'));
    await tester.pump();
    await tester.pump();
    expect(gates, hasLength(2));
    gates[1].complete({'output': 'new'});
    await tester.pump();
    gates[0].complete({'output': 'old'});
    await tester.pump();
    expect(find.textContaining('new', findRichText: true), findsWidgets);
    expect(find.textContaining('old', findRichText: true), findsNothing);
  });

  testWidgets('a finished run with new output fetches again', (tester) async {
    final (c, client) = await _pump(tester, state: 'failed');
    expect(_logCalls(client), 1);
    await c
        .read(projectsProvider.notifier)
        .load(FakeGatewayClient((m, p) async => _tree('failed', tail: 'x')));
    await tester.pump();
    expect(_logCalls(client), 2);
  });

  testWidgets('closing the page during a rerun does not fetch', (tester) async {
    final gate = Completer<Object?>();
    final client = FakeGatewayClient(
      (m, p) => m == 'workspace.runSetup'
          ? gate.future
          : Future.value({'output': ''}),
    );
    final c = ProviderContainer(
      overrides: [
        gatewayProvider.overrideWithValue(null),
        projectsApiProvider.overrideWithValue(ProjectsApi(() => client)),
      ],
    );
    addTearDown(c.dispose);
    await c
        .read(projectsProvider.notifier)
        .load(FakeGatewayClient((m, p) async => _tree('failed')));
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: c,
        child: MaterialApp(
          home: Scaffold(
            body: Builder(
              builder: (ctx) => TextButton(
                onPressed: () => Navigator.of(ctx).push(
                  MaterialPageRoute<void>(
                    builder: (_) => const SetupLogScreen(workspaceId: 'A:w2'),
                  ),
                ),
                child: const Text('open'),
              ),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    expect(_logCalls(client), 1);
    await tester.tap(find.byTooltip('Rerun setup'));
    await tester.pump();
    tester.state<NavigatorState>(find.byType(Navigator)).pop();
    await tester.pumpAndSettle();
    gate.complete(null);
    await tester.pumpAndSettle();
    expect(_logCalls(client), 1);
  });
}
