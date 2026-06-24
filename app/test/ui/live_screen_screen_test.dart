import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/models/session.dart';
import 'package:argus/ui/live_screen_screen.dart';
import '../support/fake_session_repository.dart';

class _FakeControl extends FakeSessionRepository {
  final sendKeysCalls = <List<String>>[];
  final sendRawCalls = <List<String>>[];

  @override
  Future<Result<String>> capture(String sessionId) async =>
      const Result.ok('screenX');

  @override
  Future<Result<void>> sendKeys(String sessionId, List<String> keys) async {
    sendKeysCalls.add([sessionId, ...keys]);
    return const Result.ok(null);
  }

  @override
  Future<Result<void>> sendRaw(String sessionId, String text) async {
    sendRawCalls.add([sessionId, text]);
    return const Result.ok(null);
  }
}

Session _makeSession() => Session.fromJson(
      jsonDecode(jsonEncode({
        'id': 'test-session-id',
        'tool': 't',
        'status': 'active',
        'source': 'hooked',
        'repo': 'my-repo',
        'tmux': {
          'server': 'argus',
          'pane_id': '%1',
          'session_name': 's',
          'window_index': 0,
          'current_path': '/p',
        },
      })) as Map<String, dynamic>,
    );

Future<void> _pump(
    WidgetTester tester, Session session, SessionRepository control) async {
  await tester.pumpWidget(ProviderScope(
    overrides: [sessionRepositoryProvider.overrideWithValue(control)],
    child: MaterialApp(home: LiveScreenScreen(session: session)),
  ));
}

void main() {
  testWidgets('renders captured screen content after first capture',
      (tester) async {
    final fake = _FakeControl();
    final session = _makeSession();
    await _pump(tester, session, fake);

    // Let the immediate capture future resolve.
    await tester.pump();

    expect(find.textContaining('screenX'), findsOneWidget);

    // Pump the widget away so dispose() cancels the Timer before test ends.
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('tap Enter quick-key button calls sendKeys with Enter',
      (tester) async {
    final fake = _FakeControl();
    final session = _makeSession();
    await _pump(tester, session, fake);
    await tester.pump();

    await tester.tap(find.text('↵'));
    await tester.pump();

    expect(fake.sendKeysCalls, isNotEmpty);
    final call = fake.sendKeysCalls.last;
    expect(call[0], session.id);
    expect(call[1], 'Enter');

    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('entering text and tapping Send calls sendRaw', (tester) async {
    final fake = _FakeControl();
    final session = _makeSession();
    await _pump(tester, session, fake);
    await tester.pump();

    await tester.enterText(find.byType(TextField), 'hello world');
    await tester.tap(find.text('Send'));
    await tester.pump();

    expect(fake.sendRawCalls, isNotEmpty);
    final call = fake.sendRawCalls.last;
    expect(call[0], session.id);
    expect(call[1], 'hello world');

    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('AppBar title uses repo when non-empty', (tester) async {
    final fake = _FakeControl();
    final session = _makeSession(); // repo = 'my-repo'
    await _pump(tester, session, fake);
    await tester.pump();

    expect(find.text('my-repo'), findsOneWidget);

    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('capture error is swallowed and does not surface as exception',
      (tester) async {
    final throwingControl = _ThrowingControl();
    final session = _makeSession();
    await _pump(tester, session, throwingControl);

    // Let the immediate capture future resolve (throws internally).
    await tester.pump();

    // Advance past a poll tick — should not throw.
    await tester.pump(const Duration(milliseconds: 800));

    expect(tester.takeException(), isNull);

    // Pump the widget away to cancel the timer before the test ends.
    await tester.pumpWidget(const SizedBox());
  });
}

class _ThrowingControl extends FakeSessionRepository {
  @override
  Future<Result<String>> capture(String sessionId) async =>
      Result.error(StateError('client closed'));
}
