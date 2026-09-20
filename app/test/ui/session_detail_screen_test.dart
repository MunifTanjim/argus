import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/misc.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/data/transcript_repository.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/state/transcript.dart';
import 'package:argus/state/transcript_controller.dart';
import 'package:argus/ui/interaction_bar.dart';
import 'package:argus/ui/session_detail_screen.dart';
import '../support/fake_session_repository.dart';

Session _sStarting() => Session.fromJson({
      'id': 'mac:%1',
      'agent': 't',
      'status': 'starting',
      'source': 'discovered',
      'frontend': 'tmux',
      'tmux': {
        'server': 'argus',
        'pane_id': '%1',
        'session_name': 's',
        'window_index': 0,
        'current_path': '/p',
      },
      'repo': 'argus',
      'node_label': 'mac',
    });

Session _s({String? agentSessionId, String? name, String? branch}) =>
    Session.fromJson({
      'id': 'mac:%1',
      'agent': 't',
      'status': 'working',
      'source': 'hooked',
      'frontend': 'tmux',
      'tmux': {
        'server': 'argus',
        'pane_id': '%1',
        'session_name': 's',
        'window_index': 0,
        'current_path': '/p',
      },
      'repo': 'argus',
      'branch': ?branch,
      'node_label': 'mac',
      'agent_session_id': ?agentSessionId,
      'name': ?name,
    });

class _SeededTranscript extends TranscriptNotifier {
  _SeededTranscript(this._seed);
  final List<Chunk> _seed;
  @override
  TranscriptState build() =>
      TranscriptState(subId: 'x', chunks: _seed, loaded: true);
}

class _NoopSub implements TranscriptSubscription {
  @override
  void dispose() {}
}

// Records the store each open() binds to, so a test can assert the screen
// re-opens onto the new key when the agent session id changes.
class _RecordingRepo implements TranscriptRepository {
  final List<TranscriptNotifier> stores = [];
  @override
  TranscriptSubscription? open({
    required String sessionId,
    String? agentId,
    required TranscriptNotifier store,
  }) {
    stores.add(store);
    return _NoopSub();
  }
}

class _FakeSessionControl extends FakeSessionRepository {
  final List<String> killedIds = [];

  @override
  Future<Result<void>> kill(String sessionId) async {
    killedIds.add(sessionId);
    return const Result.ok(null);
  }
}

Widget _app(List<Override> overrides, {Session? session}) => ProviderScope(
      overrides: overrides,
      child: MaterialApp(home: SessionDetailScreen(session: session ?? _s())),
    );

List<Override> _baseOverrides() => [
      gatewayProvider.overrideWithValue(null),
      transcriptProvider('mac:%1')
          .overrideWith(() => _SeededTranscript(const [])),
    ];

void main() {
  testWidgets('renders chunk feed from the store', (tester) async {
    await tester.pumpWidget(_app([
      gatewayProvider.overrideWithValue(null),
      transcriptProvider('mac:%1').overrideWith(() => _SeededTranscript(const [
            Chunk(id: 'u', kind: ChunkKind.user, text: 'hello world'),
          ])),
    ]));
    await tester.pump();
    expect(find.textContaining('hello world'), findsOneWidget);
  });

  testWidgets('empty state when no chunks', (tester) async {
    await tester.pumpWidget(_app([
      gatewayProvider.overrideWithValue(null),
      transcriptProvider('mac:%1')
          .overrideWith(() => _SeededTranscript(const [])),
    ]));
    await tester.pump();
    expect(find.textContaining('No transcript'), findsOneWidget);
  });

  testWidgets('shows the session name as an AppBar subtitle', (tester) async {
    await tester.pumpWidget(ProviderScope(
      overrides: _baseOverrides(),
      child:
          MaterialApp(home: SessionDetailScreen(session: _s(name: 'auth-refactor'))),
    ));
    await tester.pump();
    expect(find.text('argus'), findsOneWidget);
    expect(find.text('auth-refactor'), findsOneWidget);
  });

  testWidgets('shows terminal icon button in AppBar', (tester) async {
    await tester.pumpWidget(_app([
      gatewayProvider.overrideWithValue(null),
      transcriptProvider('mac:%1')
          .overrideWith(() => _SeededTranscript(const [])),
    ]));
    await tester.pump();
    expect(find.byIcon(Icons.terminal), findsOneWidget);
  });

  testWidgets('shows branch icon with branch name tooltip when set',
      (tester) async {
    await tester.pumpWidget(_app(_baseOverrides(),
        session: _s(branch: 'feat/session-git-branch')));
    await tester.pump();
    expect(find.byIcon(Icons.commit), findsOneWidget);
    expect(find.byTooltip('feat/session-git-branch'), findsOneWidget);
  });

  testWidgets('no branch icon when branch is absent', (tester) async {
    await tester.pumpWidget(_app(_baseOverrides()));
    await tester.pump();
    expect(find.byIcon(Icons.commit), findsNothing);
  });

  testWidgets('shows Changes item in overflow when branch is set',
      (tester) async {
    await tester.pumpWidget(_app(_baseOverrides(),
        session: _s(branch: 'feat/session-git-branch')));
    await tester.pump();
    await tester.tap(find.byType(PopupMenuButton<String>));
    await tester.pumpAndSettle();
    expect(find.byIcon(Icons.difference), findsOneWidget);
  });

  testWidgets('no Changes item in overflow when branch is absent',
      (tester) async {
    await tester.pumpWidget(_app(_baseOverrides()));
    await tester.pump();
    await tester.tap(find.byType(PopupMenuButton<String>));
    await tester.pumpAndSettle();
    expect(find.byIcon(Icons.difference), findsNothing);
  });

  testWidgets('AppBar has overflow PopupMenuButton', (tester) async {
    await tester.pumpWidget(_app(_baseOverrides()));
    await tester.pump();
    expect(find.byType(PopupMenuButton<String>), findsOneWidget);
  });

  testWidgets('opening overflow menu shows Kill Session item', (tester) async {
    await tester.pumpWidget(_app(_baseOverrides()));
    await tester.pump();
    await tester.tap(find.byType(PopupMenuButton<String>));
    await tester.pumpAndSettle();
    expect(find.text('Kill Session'), findsOneWidget);
  });

  testWidgets('tapping Kill Session shows confirm AlertDialog', (tester) async {
    await tester.pumpWidget(_app(_baseOverrides()));
    await tester.pump();
    await tester.tap(find.byType(PopupMenuButton<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Kill Session'));
    await tester.pumpAndSettle();
    expect(find.byType(AlertDialog), findsOneWidget);
    expect(find.text('Kill Session?'), findsOneWidget);
  });

  testWidgets('confirming kill calls SessionControl.kill with session id',
      (tester) async {
    final fake = _FakeSessionControl();
    await tester.pumpWidget(_app([
      ..._baseOverrides(),
      sessionRepositoryProvider.overrideWithValue(fake),
    ]));
    await tester.pump();
    await tester.tap(find.byType(PopupMenuButton<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Kill Session'));
    await tester.pumpAndSettle();
    // Tap the destructive 'Kill' button in the dialog
    await tester.tap(find.text('Kill').last);
    await tester.pumpAndSettle();
    expect(fake.killedIds, contains('mac:%1'));
  });

  testWidgets('re-opens onto the new store when agent session id changes',
      (tester) async {
    final repo = _RecordingRepo();
    await tester.pumpWidget(_app([
      gatewayProvider.overrideWithValue(null),
      transcriptRepositoryProvider.overrideWithValue(repo),
      transcriptProvider('mac:%1') // fallback key, before any hook
          .overrideWith(() => _SeededTranscript(const [
                Chunk(id: 'pre', kind: ChunkKind.user, text: 'pre-clear line'),
              ])),
      transcriptProvider('c9') // post-clear store
          .overrideWith(() => _SeededTranscript(const [
                Chunk(id: 'post', kind: ChunkKind.user, text: 'post-clear line'),
              ])),
    ]));
    await tester.pump();
    expect(find.textContaining('pre-clear line'), findsOneWidget);
    expect(repo.stores, hasLength(1));

    // /clear arrives: same session id, new agent session id.
    final container =
        ProviderScope.containerOf(tester.element(find.byType(SessionDetailScreen)));
    container.read(sessionsProvider.notifier).replaceAll([
      _s(agentSessionId: 'c9'),
    ]);
    await tester.pump();

    expect(find.textContaining('post-clear line'), findsOneWidget);
    expect(find.textContaining('pre-clear line'), findsNothing);
    expect(repo.stores, hasLength(2)); // re-opened onto the fresh store
  });

  // opencode: paneless, input_mode api, so it accepts prompts and can be torn
  // down via the single Kill Session action.
  Session oc({String status = 'idle', String inputMode = 'api'}) =>
      Session.fromJson({
        'id': 'oc:%1',
        'agent': 'opencode',
        'status': status,
        'source': 'hooked',
        'frontend': 'external',
        'input_mode': inputMode,
        'tmux': {'pane_id': ''},
        'repo': 'proj',
        'node_label': 'mac',
        'interaction': {'kind': 'idle'},
      });

  Widget appFor(Session s, {List<Override> extra = const []}) => ProviderScope(
        overrides: [
          gatewayProvider.overrideWithValue(null),
          transcriptProvider(s.id)
              .overrideWith(() => _SeededTranscript(const [])),
          ...extra,
        ],
        child: MaterialApp(home: SessionDetailScreen(session: s)),
      );

  testWidgets('idle opencode session shows a tappable reply bar', (tester) async {
    await tester.pumpWidget(appFor(oc()));
    await tester.pump();
    expect(find.byType(InteractionBar), findsOneWidget);
    expect(find.text('Respond'), findsOneWidget);
    expect(
        find.text("argus can't send input to this session"), findsNothing);
  });

  testWidgets('idle decision-only session shows the respond-elsewhere notice',
      (tester) async {
    final vscode = Session.fromJson({
      'id': 'vs:%1',
      'agent': 'claude',
      'status': 'awaiting_input',
      'source': 'hooked',
      'frontend': 'vscode',
      'tmux': {'pane_id': ''},
      'repo': 'proj',
      'interaction': {'kind': 'idle'},
    });
    await tester.pumpWidget(appFor(vscode));
    await tester.pump();
    expect(
        find.text("argus can't send input to this session"), findsOneWidget);
  });

  testWidgets('Kill Session is enabled for an idle opencode session and calls kill',
      (tester) async {
    final fake = _FakeSessionControl();
    await tester.pumpWidget(appFor(oc(),
        extra: [sessionRepositoryProvider.overrideWithValue(fake)]));
    await tester.pump();
    await tester.tap(find.byType(PopupMenuButton<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Kill Session'));
    await tester.pumpAndSettle();
    expect(find.byType(AlertDialog), findsOneWidget);
    await tester.tap(find.text('Kill').last);
    await tester.pumpAndSettle();
    expect(fake.killedIds, contains('oc:%1'));
  });

  testWidgets('Kill Session is enabled for a working opencode session',
      (tester) async {
    final fake = _FakeSessionControl();
    await tester.pumpWidget(appFor(oc(status: 'working'),
        extra: [sessionRepositoryProvider.overrideWithValue(fake)]));
    await tester.pump();
    await tester.tap(find.byType(PopupMenuButton<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Kill Session'));
    await tester.pumpAndSettle();
    // Kill is unconditional: the confirm dialog opens and kill is sent.
    expect(find.byType(AlertDialog), findsOneWidget);
    await tester.tap(find.text('Kill').last);
    await tester.pumpAndSettle();
    expect(fake.killedIds, contains('oc:%1'));
  });

  testWidgets('starting session shows startup notice and no input bar',
      (tester) async {
    await tester.pumpWidget(_app(
      [
        gatewayProvider.overrideWithValue(null),
        transcriptProvider('mac:%1')
            .overrideWith(() => _SeededTranscript(const [])),
      ],
      session: _sStarting(),
    ));
    await tester.pump();
    expect(find.textContaining('startup prompt'), findsOneWidget);
    expect(find.byType(InteractionBar), findsNothing);
  });
}
