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
import 'package:argus/ui/session_detail_screen.dart';
import '../support/fake_session_repository.dart';

Session _s({String? claudeSessionId}) => Session.fromJson({
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
      'node_label': 'mac',
      'claude_session_id': ?claudeSessionId,
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
// re-opens onto the new key when the ClaudeSessionID changes.
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

Widget _app(List<Override> overrides) => ProviderScope(
      overrides: overrides,
      child: MaterialApp(home: SessionDetailScreen(session: _s())),
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

  testWidgets('shows terminal icon button in AppBar', (tester) async {
    await tester.pumpWidget(_app([
      gatewayProvider.overrideWithValue(null),
      transcriptProvider('mac:%1')
          .overrideWith(() => _SeededTranscript(const [])),
    ]));
    await tester.pump();
    expect(find.byIcon(Icons.terminal), findsOneWidget);
  });

  testWidgets('AppBar has overflow PopupMenuButton', (tester) async {
    await tester.pumpWidget(_app(_baseOverrides()));
    await tester.pump();
    expect(find.byType(PopupMenuButton<String>), findsOneWidget);
  });

  testWidgets('opening overflow menu shows Kill session item', (tester) async {
    await tester.pumpWidget(_app(_baseOverrides()));
    await tester.pump();
    await tester.tap(find.byType(PopupMenuButton<String>));
    await tester.pumpAndSettle();
    expect(find.text('Kill session'), findsOneWidget);
  });

  testWidgets('tapping Kill session shows confirm AlertDialog', (tester) async {
    await tester.pumpWidget(_app(_baseOverrides()));
    await tester.pump();
    await tester.tap(find.byType(PopupMenuButton<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Kill session'));
    await tester.pumpAndSettle();
    expect(find.byType(AlertDialog), findsOneWidget);
    expect(find.text('Kill session?'), findsOneWidget);
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
    await tester.tap(find.text('Kill session'));
    await tester.pumpAndSettle();
    // Tap the destructive 'Kill' button in the dialog
    await tester.tap(find.text('Kill').last);
    await tester.pumpAndSettle();
    expect(fake.killedIds, contains('mac:%1'));
  });

  // A /clear sets a new ClaudeSessionID under the open session: the feed must
  // switch to the fresh (post-clear) store and re-open the subscription onto it,
  // never showing pre-clear chunks.
  testWidgets('re-opens onto the new store when ClaudeSessionID changes',
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

    // /clear arrives: same session id, new ClaudeSessionID.
    final container =
        ProviderScope.containerOf(tester.element(find.byType(SessionDetailScreen)));
    container.read(sessionsProvider.notifier).replaceAll([
      _s(claudeSessionId: 'c9'),
    ]);
    await tester.pump();

    expect(find.textContaining('post-clear line'), findsOneWidget);
    expect(find.textContaining('pre-clear line'), findsNothing);
    expect(repo.stores, hasLength(2)); // re-opened onto the fresh store
  });
}
