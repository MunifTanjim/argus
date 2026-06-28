import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/misc.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/transcript.dart';
import 'package:argus/state/transcript_controller.dart';
import 'package:argus/ui/session_detail_screen.dart';
import '../support/fake_session_repository.dart';

Session _s() => Session.fromJson(jsonDecode(
        '{"id":"mac:%1","tool":"t","status":"working","source":"hooked","frontend":"tmux","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"repo":"argus","node_label":"mac"}')
    as Map<String, dynamic>);

class _SeededTranscript extends TranscriptNotifier {
  _SeededTranscript(this._seed);
  final List<Chunk> _seed;
  @override
  TranscriptState build() =>
      TranscriptState(subId: 'x', chunks: _seed, loaded: true);
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
}
