import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/data/terminal_repository.dart';
import 'package:argus/models/enums.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/terminal_controller.dart';
import 'package:argus/transport/connection.dart';
import 'package:argus/ui/live_screen_screen.dart';

class _FakeSession implements TerminalSession {
  _FakeSession(this.sends);
  final List<List<int>> sends;
  @override
  void send(List<int> data) => sends.add(data);
  @override
  void resize(int cols, int rows) {}
  @override
  void dispose() {}
}

class _FakeTerminalRepo implements TerminalRepository {
  _FakeTerminalRepo({this.returnNull = false});
  // When true, open() returns null to mimic being disconnected (no client).
  final bool returnNull;
  final sends = <List<int>>[];
  void Function(List<int>)? onData;
  void Function(TerminalExitReason reason)? onExited;
  int openCount = 0;

  @override
  TerminalSession? open({
    required String sessionId,
    required int cols,
    required int rows,
    required void Function(List<int> data) onData,
    void Function(TerminalExitReason reason)? onExited,
    void Function(Object error)? onError,
  }) {
    openCount++;
    this.onData = onData;
    this.onExited = onExited;
    return returnNull ? null : _FakeSession(sends);
  }
}

Session _makeSession() => Session.fromJson(
      jsonDecode(jsonEncode({
        'id': 'test-session-id',
        'agent': 't',
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

Future<void> _pump(WidgetTester tester, _FakeTerminalRepo repo) async {
  await tester.pumpWidget(ProviderScope(
    overrides: [terminalRepositoryProvider.overrideWithValue(repo)],
    child: MaterialApp(home: LiveScreenScreen(session: _makeSession())),
  ));
  await tester.pump(); // run the post-frame _open()
}

// Pushes the screen onto a real navigator so Navigator.maybePop() has a route
// to pop (the disconnect UX), unlike _pump which mounts it as the root home.
Future<void> _pumpPushed(WidgetTester tester, _FakeTerminalRepo repo) async {
  await tester.pumpWidget(ProviderScope(
    overrides: [terminalRepositoryProvider.overrideWithValue(repo)],
    child: MaterialApp(
      home: Builder(
        builder: (context) => Scaffold(
          body: Center(
            child: ElevatedButton(
              onPressed: () => Navigator.of(context).push(MaterialPageRoute(
                  builder: (_) => LiveScreenScreen(session: _makeSession()))),
              child: const Text('go'),
            ),
          ),
        ),
      ),
    ),
  ));
  await tester.tap(find.text('go'));
  await tester.pumpAndSettle();
  await tester.pump(); // run the post-frame _open()
}

void main() {
  testWidgets('opens an attach after first frame', (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);
    expect(repo.openCount, 1);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('tapping Enter sends CR as raw bytes', (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);

    await tester.tap(find.byTooltip('Enter'));
    await tester.pump();

    expect(repo.sends.last, [13]);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('arming Ctrl then typing a sends Ctrl+A (0x01)', (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);

    await tester.tap(find.byTooltip('Ctrl')); // arm the one-shot Ctrl modifier
    await tester.pump();
    await tester.enterText(find.byType(TextField), 'a');
    await tester.pump();

    expect(repo.sends.last, [1]);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('tapping a keycap keeps the keyboard up (modifier+type flow)',
      (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);
    await tester.tap(find.byType(TextField));
    await tester.pump();
    expect(tester.testTextInput.isVisible, isTrue,
        reason: 'keyboard up after focusing the field');
    await tester.tap(find.byTooltip('Ctrl'));
    await tester.pump();
    expect(tester.testTextInput.isVisible, isTrue,
        reason: 'tapping a keycap must not dismiss the keyboard');
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('entering text and tapping Send sends utf8 bytes', (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);

    await tester.enterText(find.byType(TextField), 'ls');
    await tester.tap(find.text('Send'));
    await tester.pump();

    expect(repo.sends.last, utf8.encode('ls'));
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('streamed output does not throw', (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);

    repo.onData!(utf8.encode('hello\r\n'));
    await tester.pump();

    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('leaves the screen on disconnect without re-attaching',
      (tester) async {
    final repo = _FakeTerminalRepo();
    await _pumpPushed(tester, repo);
    expect(find.byType(LiveScreenScreen), findsOneWidget);
    expect(repo.openCount, 1);

    final container = ProviderScope.containerOf(
        tester.element(find.byType(LiveScreenScreen)));
    // Enter connected, then drop: the screen pops (matches the TUI) and there
    // is no silent re-attach.
    container.read(connStateProvider.notifier).state = ConnState.connected;
    await tester.pump();
    container.read(connStateProvider.notifier).state = ConnState.reconnecting;
    await tester.pumpAndSettle();

    expect(find.byType(LiveScreenScreen), findsNothing);
    expect(repo.openCount, 1);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('eviction shows "opened elsewhere" and leaves the screen',
      (tester) async {
    final repo = _FakeTerminalRepo();
    await _pumpPushed(tester, repo);
    expect(find.byType(LiveScreenScreen), findsOneWidget);

    repo.onExited!(TerminalExitReason.evicted);
    await tester.pump(); // show the snackbar and start the pop
    expect(find.text('terminal opened elsewhere'), findsAtLeastNWidgets(1));

    await tester.pumpAndSettle();
    expect(find.byType(LiveScreenScreen), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('shows "not connected" when open returns null', (tester) async {
    final repo = _FakeTerminalRepo(returnNull: true);
    await _pump(tester, repo);
    expect(repo.openCount, 1);
    expect(find.text('not connected'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('renders all 16 keys across two rows', (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);

    for (final tip in const [
      'Escape', 'Tab', 'Home', 'End', 'PageUp', 'Up', 'PageDown', 'Backspace',
      'Delete', 'Shift', 'Ctrl', 'Alt', 'Left', 'Down', 'Right', 'Enter',
    ]) {
      expect(find.byTooltip(tip), findsOneWidget, reason: 'missing key: $tip');
    }
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('Alt+Enter sends ESC+CR (meta-Enter for newline)', (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);
    await tester.tap(find.byTooltip('Alt')); // arm the one-shot Alt modifier
    await tester.pump();
    await tester.tap(find.byTooltip('Enter'));
    await tester.pump();
    expect(repo.sends.last, [27, 13]);
    await tester.pumpWidget(const SizedBox());
  });
  testWidgets('Shift+Enter sends ESC+CR (meta-Enter for newline)',
      (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);
    await tester.tap(find.byTooltip('Shift'));
    await tester.pump();
    await tester.tap(find.byTooltip('Enter'));
    await tester.pump();
    expect(repo.sends.last, [27, 13]);
    await tester.pumpWidget(const SizedBox());
  });
  testWidgets('tapping Home sends the home escape sequence', (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);
    await tester.tap(find.byTooltip('Home'));
    await tester.pump();
    expect(repo.sends.last, [27, 91, 72]);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('tapping End sends the end escape sequence', (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);
    await tester.tap(find.byTooltip('End'));
    await tester.pump();
    expect(repo.sends.last, [27, 91, 70]);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('tapping PageUp sends the pgup escape sequence', (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);
    await tester.tap(find.byTooltip('PageUp'));
    await tester.pump();
    expect(repo.sends.last, [27, 91, 53, 126]);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('tapping PageDown sends the pgdown escape sequence',
      (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);
    await tester.tap(find.byTooltip('PageDown'));
    await tester.pump();
    expect(repo.sends.last, [27, 91, 54, 126]);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('tapping Delete sends the delete escape sequence',
      (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);
    await tester.tap(find.byTooltip('Delete'));
    await tester.pump();
    expect(repo.sends.last, [27, 91, 51, 126]);
    await tester.pumpWidget(const SizedBox());
  });
}
