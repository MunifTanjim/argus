import 'dart:convert';

import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/models/entry.dart';
import 'package:argus/models/bot.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/approval_text.dart';
import 'package:argus/state/bots.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/ui/bot_screen.dart';
import 'package:argus/ui/interaction_panel.dart';
import 'package:argus/ui/thread_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';
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

Session _session(Map<String, dynamic> interaction) => Session.fromJson(
  jsonDecode(
        jsonEncode({
          'id': 'A:s1',
          'agent': 'claude',
          'status': 'awaiting_input',
          'source': 'hooked',
          'tmux': {
            'server': 'argus',
            'pane_id': '%1',
            'session_name': 's',
            'window_index': 0,
            'current_path': '/p',
          },
          'interaction': interaction,
        }),
      )
      as Map<String, dynamic>,
);

Future<void> _pumpPanel(WidgetTester tester, Session s, _Repo repo) async {
  tester.view.physicalSize = const Size(500, 1400);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(
    ProviderScope(
      overrides: [sessionRepositoryProvider.overrideWithValue(repo)],
      child: MaterialApp(
        home: Scaffold(
          body: SizedBox(height: 900, child: InteractionPanel(session: s)),
        ),
      ),
    ),
  );
  await tester.pump();
}

final _card = {
  'kind': 'permission',
  'tool_name': 'Bash',
  'tool_input': jsonEncode({'command': 'git push --force origin main'}),
  'request_id': 'req-1',
  'classes': ['irreversible', 'outward', 'security', 'unknown'],
  'started_by': 'routine',
  'options': [
    {'label': 'Allow once', 'value': 'allow'},
    {'label': 'Allow in this thread', 'value': 'allow-thread'},
    {'label': 'Always allow', 'value': 'allow-always'},
    {'label': 'Deny', 'value': 'deny', 'reject': true},
    {'label': 'Explain', 'value': 'explain'},
  ],
};

final _secret = {
  'kind': 'secret',
  'message': 'Token for the GitHub CLI',
  'secret_name': 'GH_TOKEN',
  'request_id': 'secret:GH_TOKEN',
  'options': [
    {'label': 'Save', 'value': 'save'},
    {'label': 'Decline', 'value': 'decline', 'reject': true},
  ],
};

Map<String, dynamic> _argus(String id, String kind, Map<String, dynamic> payload, int eventId,
        {String detail = ''}) =>
    {
      'id': id,
      'kind': 'argus',
      'timestamp': '2026-11-22T10:00:0${eventId}Z',
      'label': kind,
      if (detail.isNotEmpty) 'detail': detail,
      'event': {'id': eventId, 'kind': kind, 'payload': payload},
    };

void main() {
  test('class warnings and who started the work, in words', () {
    expect(classWarnings(['irreversible', 'security', 'outward', 'x']), [
      "can't be undone",
      'changes security settings',
      'sends data out of this machine',
    ]);
    expect(startedByLabel('you'), '');
    expect(startedByLabel(null), '');
    expect(startedByLabel('routine'), 'started by a routine');
    expect(startedByLabel('check-back'), 'started by a check-back');
    expect(startedByLabel('x'), 'started by x');
  });

  testWidgets('an approval card shows warnings, starter, operation and options', (tester) async {
    final repo = _Repo();
    await _pumpPanel(tester, _session(_card), repo);
    expect(find.text("⚠ can't be undone"), findsOneWidget);
    expect(find.text('⚠ sends data out of this machine'), findsOneWidget);
    expect(find.text('⚠ changes security settings'), findsOneWidget);
    expect(find.text('started by a routine'), findsOneWidget);
    expect(find.textContaining('git push --force origin main'), findsWidgets);
    for (final o in ['Allow once', 'Allow in this thread', 'Always allow', 'Deny', 'Explain']) {
      expect(find.text(o), findsOneWidget, reason: o);
    }
    await tester.tap(find.text('Allow in this thread'));
    await tester.pump();
    expect(repo.calls.single, {
      'session_id': 'A:s1',
      'kind': 'permission',
      'request_id': 'req-1',
      'option_value': 'allow-thread',
    });
  });

  testWidgets('a card from the user shows no starter line', (tester) async {
    await _pumpPanel(
      tester,
      _session({..._card, 'started_by': 'you', 'classes': []}),
      _Repo(),
    );
    expect(find.textContaining('started by'), findsNothing);
    expect(find.textContaining('⚠'), findsNothing);
  });

  testWidgets('Explain is sent as its option value', (tester) async {
    final repo = _Repo();
    await _pumpPanel(tester, _session(_card), repo);
    await tester.tap(find.text('Explain'));
    await tester.pump();
    expect(repo.calls.single['option_value'], 'explain');
  });

  testWidgets('a secret request has a hidden field and sends the value once', (tester) async {
    final repo = _Repo();
    await _pumpPanel(tester, _session(_secret), repo);
    expect(find.text('Secret requested · GH_TOKEN'), findsOneWidget);
    expect(find.textContaining('the bot sees only \$GH_TOKEN'), findsOneWidget);
    final field = tester.widget<TextField>(find.byKey(const ValueKey('secret-value')));
    expect(field.obscureText, true);
    expect(field.enableSuggestions, false);
    expect(field.autocorrect, false);

    // Save needs a value.
    expect(
      tester.widget<FilledButton>(find.widgetWithText(FilledButton, 'Save')).onPressed,
      isNull,
    );
    await tester.enterText(find.byKey(const ValueKey('secret-value')), 's3cret');
    await tester.pump();
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();
    expect(repo.calls.single, {
      'session_id': 'A:s1',
      'kind': 'secret',
      'request_id': 'secret:GH_TOKEN',
      'option_value': 'save',
      'secret_value': 's3cret',
    });
    // The field is emptied once the value is sent.
    expect(find.text('s3cret'), findsNothing);
    expect(
      tester.widget<TextField>(find.byKey(const ValueKey('secret-value'))).controller!.text,
      isEmpty,
    );
  });

  testWidgets('declining a secret sends no value', (tester) async {
    final repo = _Repo();
    await _pumpPanel(tester, _session(_secret), repo);
    await tester.enterText(find.byKey(const ValueKey('secret-value')), 'typed');
    await tester.tap(find.text('Decline'));
    await tester.pump();
    expect(repo.calls.single, {
      'session_id': 'A:s1',
      'kind': 'secret',
      'request_id': 'secret:GH_TOKEN',
      'option_value': 'decline',
    });
  });

  testWidgets('a failed save shows the server error', (tester) async {
    final repo = _Repo()..failWith = const RpcError(-32000, 'name must be like GH_TOKEN');
    await _pumpPanel(tester, _session(_secret), repo);
    await tester.enterText(find.byKey(const ValueKey('secret-value')), 'v');
    await tester.pump();
    await tester.tap(find.text('Save'));
    await tester.pump();
    await tester.pump();
    expect(find.text('Failed: name must be like GH_TOKEN'), findsOneWidget);
    // The error does not echo the value.
    expect(find.textContaining('secret_value'), findsNothing);
  });

  group('drafts', () {
    Future<FakeGatewayClient> pump(WidgetTester tester, List<Map<String, dynamic>> entries,
        {Object? Function(String, Object?)? extra}) async {
      tester.view.physicalSize = const Size(500, 1000);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      final client = FakeGatewayClient((m, p) async {
        final x = extra?.call(m, p);
        if (x != null) return x;
        switch (m) {
          case 'bot.thread.timeline':
            return {'thread_id': 'A:t1', 'entries': entries, 'sessions': []};
          case 'bot.list':
            return {
              'bots': [
                {'id': 'A:a1', 'name': 'scout', 'status': 'idle'},
              ],
            };
          case 'bot.thread.list':
            return {'threads': []};
        }
        return null;
      });
      final c = ProviderContainer(
        overrides: [botsClientProvider.overrideWithValue(client)],
      );
      addTearDown(c.dispose);
      await c.read(botsProvider.notifier).load(client);
      await tester.pumpWidget(
        UncontrolledProviderScope(
          container: c,
          child: const MaterialApp(
            home: ThreadScreen(botId: 'A:a1', threadId: 'A:t1', title: 'Chat'),
          ),
        ),
      );
      await tester.pumpAndSettle();
      return client;
    }

    final draft = _argus('e1', 'draft', {'tool': 'Bash', 'command': 'git push'}, 7,
        detail: 'Drafted Bash: git push');

    test('draft ids and decisions', () {
      Entry e(Map<String, dynamic> j) => Entry.fromJson(j);
      expect(draftIdOf(e(draft)), 7);
      expect(draftIdOf(e(_argus('x', 'offer', {}, 3))), 0);
      expect(
        decidedDrafts([
          e(_argus('a', 'draft.sent', {'event_id': 7}, 8)),
          e(_argus('b', 'draft.discarded', {'event_id': 9}, 9)),
          e(draft),
        ]),
        {7, 9},
      );
    });

    testWidgets('an open draft can be sent', (tester) async {
      final client = await pump(tester, [draft]);
      expect(find.text('  Drafted Bash: git push'), findsOneWidget);
      await tester.tap(find.text('Send'));
      await tester.pumpAndSettle();
      expect(
        client.calls.firstWhere((c) => c.$1 == 'bot.draft.send').$2,
        {'thread_id': 'A:t1', 'event_id': 7},
      );
    });

    testWidgets('an open draft can be discarded', (tester) async {
      final client = await pump(tester, [draft]);
      await tester.tap(find.text('Discard'));
      await tester.pumpAndSettle();
      expect(
        client.calls.firstWhere((c) => c.$1 == 'bot.draft.discard').$2,
        {'thread_id': 'A:t1', 'event_id': 7},
      );
    });

    testWidgets('a decided draft has no buttons', (tester) async {
      await pump(tester, [
        draft,
        _argus('e2', 'draft.sent', {'event_id': 7}, 8, detail: 'Ran approved command'),
      ]);
      expect(find.text('Send'), findsNothing);
      expect(find.text('Discard'), findsNothing);
    });

    testWidgets('a failed send shows the server error', (tester) async {
      await pump(
        tester,
        [draft],
        extra: (m, p) => m == 'bot.draft.send'
            ? throw const RpcError(-32000, 'draft already sent')
            : null,
      );
      await tester.tap(find.text('Send'));
      await tester.pumpAndSettle();
      expect(find.text('send: draft already sent'), findsOneWidget);
    });
  });

  testWidgets('the bot screen lists approval activity and filters unattended', (tester) async {
    tester.view.physicalSize = const Size(500, 2400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final client = FakeGatewayClient((m, p) async {
      switch (m) {
        case 'bot.list':
          return {
            'bots': [
              {'id': 'A:a1', 'name': 'scout', 'status': 'idle'},
            ],
          };
        case 'bot.activity':
          return {
            'activity': [
              {
                'id': 1,
                'started_by': (p as Map)['unattended'] == true ? 'routine' : 'you',
                'tool': 'Bash',
                'target': 'git push',
                'decision': 'deny',
                'decided_by': 'floor',
                'created_at': DateTime.now().toUtc().toIso8601String(),
              },
            ],
          };
      }
      return null;
    });
    final c = ProviderContainer(
      overrides: [botsClientProvider.overrideWithValue(client)],
    );
    addTearDown(c.dispose);
    await c.read(botsProvider.notifier).load(client);
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: c,
        child: const MaterialApp(home: BotScreen(botId: 'A:a1')),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('Activity'), findsOneWidget);
    expect(find.text('Bash git push'), findsOneWidget);
    expect(find.text('deny by floor · just now'), findsOneWidget);

    await tester.tap(find.text('Unattended only'));
    await tester.pumpAndSettle();
    expect(
      client.calls.where((c) => c.$1 == 'bot.activity').last.$2,
      {'bot_id': 'A:a1', 'unattended': true, 'limit': 20},
    );
    expect(find.text('deny by floor · started by a routine · just now'), findsOneWidget);
  });
}
