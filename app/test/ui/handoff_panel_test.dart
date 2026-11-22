import 'dart:convert';

import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/models/enums.dart';
import 'package:argus/models/session.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/ui/interaction_panel.dart';
import 'package:argus/ui/session_composer.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_session_repository.dart';

class _Repo extends FakeSessionRepository {
  final calls = <Map<String, dynamic>>[];
  Object? failWith;

  @override
  Future<Result<void>> respond(Map<String, dynamic> params) async {
    calls.add(params);
    final f = failWith;
    return f == null ? const Result.ok(null) : Result.error(f);
  }
}

class _Pushes extends NavigatorObserver {
  final routes = <Route<dynamic>>[];
  @override
  void didPush(Route<dynamic> route, Route<dynamic>? previous) =>
      routes.add(route);
}

Session _handoff({bool terminal = true}) => Session.fromJson(
  jsonDecode(
        jsonEncode({
          'id': 'A:s1',
          'agent': 'claude',
          'status': 'awaiting_input',
          'source': 'hooked',
          'can_open_terminal': terminal,
          'tmux': {
            'server': 'argus',
            'pane_id': '%1',
            'session_name': 's',
            'window_index': 0,
            'current_path': '/p',
          },
          'interaction': {
            'kind': 'handoff',
            'message': 'Log in to the registry',
            'steps': 'Run `npm login` in the terminal.',
            'request_id': 'handoff',
            'options': [
              {'label': 'Take over', 'value': 'takeover'},
              {'label': 'Done', 'value': 'done'},
            ],
          },
        }),
      )
      as Map<String, dynamic>,
);

Future<_Pushes> _pump(WidgetTester tester, Session s, _Repo repo) async {
  final obs = _Pushes();
  await tester.pumpWidget(
    ProviderScope(
      overrides: [sessionRepositoryProvider.overrideWithValue(repo)],
      child: MaterialApp(
        navigatorObservers: [obs],
        home: Scaffold(
          body: SizedBox(height: 600, child: InteractionPanel(session: s)),
        ),
      ),
    ),
  );
  await tester.pump();
  return obs;
}

void main() {
  test('a handoff parses with its steps and is pending', () {
    final s = _handoff();
    expect(s.interaction?.kind, InteractionKind.handoff);
    expect(s.interaction?.steps, 'Run `npm login` in the terminal.');
    expect(s.interactionPending, true);
    expect(interactionLabel(s.interaction!), 'Handoff');
  });

  testWidgets('shows the reason, steps and both choices', (tester) async {
    await _pump(tester, _handoff(), _Repo());
    expect(find.text('Handoff'), findsOneWidget);
    expect(find.textContaining('Log in to the registry'), findsOneWidget);
    expect(find.textContaining('npm login'), findsOneWidget);
    expect(find.widgetWithText(FilledButton, 'Take over'), findsOneWidget);
    expect(find.widgetWithText(OutlinedButton, 'Done'), findsOneWidget);
  });

  testWidgets('Done responds with the done option and the request id', (tester) async {
    final repo = _Repo();
    await _pump(tester, _handoff(), repo);
    await tester.tap(find.text('Done'));
    await tester.pump();
    expect(repo.calls.single, {
      'session_id': 'A:s1',
      'kind': 'handoff',
      'request_id': 'handoff',
      'option_value': 'done',
    });
  });

  testWidgets('a failed Done shows the server error text', (tester) async {
    final repo = _Repo()..failWith = const RpcError(-32000, 'answer the agent\'s pending request first');
    await _pump(tester, _handoff(), repo);
    await tester.tap(find.text('Done'));
    await tester.pump();
    await tester.pump();
    expect(
      find.text('Failed: answer the agent\'s pending request first'),
      findsOneWidget,
    );
  });

  testWidgets('Take over opens the terminal screen and does not respond', (tester) async {
    final repo = _Repo();
    final obs = await _pump(tester, _handoff(), repo);
    final before = obs.routes.length;
    await tester.tap(find.text('Take over'));
    await tester.pump();
    expect(obs.routes.length, before + 1);
    expect(repo.calls, isEmpty);
    tester.takeException(); // the pushed live screen needs a terminal
    expect(find.widgetWithText(FilledButton, 'Done'), findsOneWidget);
    expect(find.widgetWithText(OutlinedButton, 'Take over'), findsOneWidget);
  });

  testWidgets('Take over without a terminal says so', (tester) async {
    final repo = _Repo();
    final obs = await _pump(tester, _handoff(terminal: false), repo);
    final before = obs.routes.length;
    await tester.tap(find.text('Take over'));
    await tester.pump();
    expect(obs.routes.length, before);
    expect(find.text('No terminal to take over'), findsOneWidget);
  });
}
