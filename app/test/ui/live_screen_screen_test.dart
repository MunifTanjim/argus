import 'dart:convert';
import 'package:flterm/flterm.dart' as gt;
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:xterm/xterm.dart';
import 'package:argus/data/terminal_repository.dart';
import 'package:argus/models/enums.dart';
import 'package:argus/models/session.dart';
import 'package:argus/models/terminal.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/terminal_controller.dart';
import 'package:argus/state/terminal_prefs.dart';
import 'package:argus/state/terminals.dart';
import 'package:argus/transport/gateway_client.dart';
import 'package:argus/transport/connection.dart';
import 'package:argus/ui/live_emulator.dart';
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
  void Function(Object error)? onError;
  int openCount = 0;
  String? lastSessionId, lastTerminalId;

  @override
  TerminalSession? open({
    String? sessionId,
    String? terminalId,
    required int cols,
    required int rows,
    required void Function(List<int> data) onData,
    void Function(TerminalExitReason reason)? onExited,
    void Function(Object error)? onError,
  }) {
    openCount++;
    lastSessionId = sessionId;
    lastTerminalId = terminalId;
    this.onData = onData;
    this.onExited = onExited;
    this.onError = onError;
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

  testWidgets('input field is multi-line and grows to three lines',
      (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);

    final field = tester.widget<TextField>(find.byType(TextField));
    expect(field.minLines, 1);
    expect(field.maxLines, 3);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('multi-line text sends with the newline preserved',
      (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);

    await tester.enterText(find.byType(TextField), 'a\nb');
    await tester.tap(find.text('Send'));
    await tester.pump();

    expect(repo.sends.last, utf8.encode('a\nb'));
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

  testWidgets('arming Ctrl then tapping Home sends CSI 1;5H', (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);
    await tester.tap(find.byTooltip('Ctrl')); // arm the one-shot Ctrl modifier
    await tester.pump();
    await tester.tap(find.byTooltip('Home'));
    await tester.pump();
    expect(repo.sends.last, [27, 91, 49, 59, 53, 72]);
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

  testWidgets('a terminal target opens by terminal id with its title', (tester) async {
    final repo = _FakeTerminalRepo();
    await tester.pumpWidget(ProviderScope(
      overrides: [terminalRepositoryProvider.overrideWithValue(repo)],
      child: MaterialApp(
        home: LiveScreenScreen(
          terminal: NodeTerminal.fromJson({
            'id': 'A:@1', 'name': 'build', 'command': 'make', 'cwd': '~',
            'node_id': 'A', 'node_label': 'home',
          }),
        ),
      ),
    ));
    await tester.pump();
    expect(repo.lastTerminalId, 'A:@1');
    expect(repo.lastSessionId, isNull);
    expect(find.text('build · home'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('a failed terminal open reloads the terminal list', (tester) async {
    final repo = _FakeTerminalRepo();
    final terms = _CountingTerminals();
    await tester.pumpWidget(ProviderScope(
      overrides: [
        terminalRepositoryProvider.overrideWithValue(repo),
        terminalsProvider.overrideWith(() => terms),
      ],
      child: const MaterialApp(
        home: LiveScreenScreen(terminal: NodeTerminal(id: 'A:@1', command: 'zsh')),
      ),
    ));
    await tester.pump();
    repo.onError!(StateError('unknown terminal: @1'));
    await tester.pump();
    expect(terms.loads, 1);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('switches to another terminal of the same workspace', (tester) async {
    final repo = _FakeTerminalRepo();
    const a = NodeTerminal(id: 'A:@1', name: 'build', workspaceId: 'A:w1');
    const b = NodeTerminal(id: 'A:@2', name: 'logs', workspaceId: 'A:w1');
    const c = NodeTerminal(id: 'A:@3', name: 'other', workspaceId: 'A:w2');
    await tester.pumpWidget(ProviderScope(
      overrides: [
        terminalRepositoryProvider.overrideWithValue(repo),
        terminalsProvider.overrideWith(() => _FixedTerminals([a, b, c])),
      ],
      child: const MaterialApp(home: LiveScreenScreen(terminal: a)),
    ));
    await tester.pump();
    expect(repo.lastTerminalId, 'A:@1');

    await tester.tap(find.byTooltip('Switch terminal'));
    await tester.pumpAndSettle();
    expect(find.text('other'), findsNothing);
    await tester.tap(find.text('logs'));
    await tester.pumpAndSettle();
    await tester.pump();

    expect(repo.lastTerminalId, 'A:@2');
    expect(repo.openCount, 2);
    expect(find.text('logs'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('no switch for a terminal alone in its workspace', (tester) async {
    const a = NodeTerminal(id: 'A:@1', name: 'build', workspaceId: 'A:w1');
    const c = NodeTerminal(id: 'A:@3', name: 'other', workspaceId: 'A:w2');
    await tester.pumpWidget(ProviderScope(
      overrides: [
        terminalRepositoryProvider.overrideWithValue(_FakeTerminalRepo()),
        terminalsProvider.overrideWith(() => _FixedTerminals([a, c])),
      ],
      child: const MaterialApp(home: LiveScreenScreen(terminal: a)),
    ));
    await tester.pump();
    expect(find.byTooltip('Switch terminal'), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  test('the mouse handler forwards wheel and button events', () {
    final wheels = <bool>[];
    final buttons = <(int, bool, int, int)>[];
    final h = LiveScreenMouseHandler(
      onWheel: (up, pos) => wheels.add(up),
      onButton: (b, down, pos) => buttons.add((b, down, pos.x, pos.y)),
    );
    TerminalMouseEvent ev(TerminalMouseButton b, TerminalMouseButtonState s) => TerminalMouseEvent(
          button: b,
          buttonState: s,
          position: const CellOffset(3, 1),
          state: Terminal(),
          platform: TerminalTargetPlatform.android,
        );
    h(ev(TerminalMouseButton.wheelUp, TerminalMouseButtonState.down));
    h(ev(TerminalMouseButton.left, TerminalMouseButtonState.down));
    h(ev(TerminalMouseButton.left, TerminalMouseButtonState.up));
    h(ev(TerminalMouseButton.right, TerminalMouseButtonState.down));
    expect(wheels, [true]);
    expect(buttons, [(0, true, 3, 1), (0, false, 3, 1), (2, true, 3, 1)]);
  });

  Future<Terminal> pumpTerminal(WidgetTester tester, _FakeTerminalRepo repo) async {
    await tester.pumpWidget(ProviderScope(
      overrides: [terminalRepositoryProvider.overrideWithValue(repo)],
      child: const MaterialApp(
        home: LiveScreenScreen(terminal: NodeTerminal(id: 'A:@1', command: 'zsh')),
      ),
    ));
    await tester.pump();
    return tester.widget<TerminalView>(find.byType(TerminalView)).terminal;
  }

  testWidgets('a terminal takes keys from the keyboard, batched', (tester) async {
    final repo = _FakeTerminalRepo();
    final term = await pumpTerminal(tester, repo);
    expect(find.byType(TextField), findsNothing);
    expect(find.text('Send'), findsNothing);
    expect(tester.widget<TerminalView>(find.byType(TerminalView)).readOnly, isFalse);
    term.textInput('l');
    term.textInput('s');
    expect(repo.sends, isEmpty);
    await tester.pump(const Duration(milliseconds: 20));
    expect(repo.sends.map(utf8.decode), ['ls']);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('tapping the terminal brings back a hidden keyboard', (tester) async {
    await pumpTerminal(tester, _FakeTerminalRepo());
    await tester.tap(find.byType(TerminalView));
    await tester.pump(const Duration(milliseconds: 500));
    expect(tester.testTextInput.isVisible, isTrue);

    tester.testTextInput.hide();
    await tester.tap(find.byType(TerminalView));
    await tester.pump(const Duration(milliseconds: 500));
    expect(tester.testTextInput.isVisible, isTrue);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('an armed Ctrl applies to the next key typed on the keyboard', (tester) async {
    final repo = _FakeTerminalRepo();
    final term = await pumpTerminal(tester, repo);
    await tester.tap(find.byTooltip('Ctrl'));
    await tester.pump();
    term.textInput('c');
    await tester.pump(const Duration(milliseconds: 20));
    expect(repo.sends, [
      [0x03],
    ]);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('a session keeps the text box', (tester) async {
    final repo = _FakeTerminalRepo();
    await _pump(tester, repo);
    expect(find.byType(TextField), findsOneWidget);
    expect(find.text('Send'), findsOneWidget);
    expect(tester.widget<TerminalView>(find.byType(TerminalView)).readOnly, isTrue);
    await tester.pumpWidget(const SizedBox());
  });

  Future<gt.TerminalController> pumpGhostty(WidgetTester tester, _FakeTerminalRepo repo,
      {required bool terminal}) async {
    await tester.pumpWidget(ProviderScope(
      overrides: [
        terminalRepositoryProvider.overrideWithValue(repo),
        terminalPrefsProvider.overrideWith(() => _FixedPrefs(TerminalEmulator.ghostty)),
      ],
      child: MaterialApp(
        home: terminal
            ? const LiveScreenScreen(terminal: NodeTerminal(id: 'A:@1', command: 'zsh'))
            : LiveScreenScreen(session: _makeSession()),
      ),
    ));
    await tester.pump();
    expect(find.byType(TerminalView), findsNothing);
    return tester.widget<gt.TerminalView>(find.byType(gt.TerminalView)).controller;
  }

  testWidgets('Ghostty: a terminal takes keys from the keyboard, batched', (tester) async {
    final repo = _FakeTerminalRepo();
    final c = await pumpGhostty(tester, repo, terminal: true);
    expect(find.byType(TextField), findsNothing);
    c.sendText('l');
    c.sendText('s');
    expect(repo.sends, isEmpty);
    await tester.pump(const Duration(milliseconds: 20));
    expect(repo.sends.map(utf8.decode), ['ls']);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('Ghostty: a session keeps the text box and sends no typed keys', (tester) async {
    final repo = _FakeTerminalRepo();
    final c = await pumpGhostty(tester, repo, terminal: false);
    expect(find.byType(TextField), findsOneWidget);
    expect(tester.widget<gt.TerminalView>(find.byType(gt.TerminalView)).showKeyboard, isFalse);
    c.sendText('x');
    await tester.pump(const Duration(milliseconds: 20));
    expect(repo.sends, isEmpty);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('Ghostty: key bar keys go through the emulator on both screens', (tester) async {
    for (final terminal in [true, false]) {
      final repo = _FakeTerminalRepo();
      await pumpGhostty(tester, repo, terminal: terminal);
      await tester.tap(find.byTooltip('Up'));
      await tester.pump(const Duration(milliseconds: 20));
      expect(repo.sends.map(utf8.decode), ['\x1b[A'], reason: terminal ? 'terminal' : 'session');
      await tester.pumpWidget(const SizedBox());
    }
  });

  testWidgets('Ghostty: tapping the terminal brings back a hidden keyboard', (tester) async {
    await pumpGhostty(tester, _FakeTerminalRepo(), terminal: true);
    await tester.tap(find.byType(gt.TerminalView));
    await tester.pump(const Duration(milliseconds: 500));
    expect(tester.testTextInput.isVisible, isTrue);

    tester.testTextInput.hide();
    await tester.tap(find.byType(gt.TerminalView));
    await tester.pump(const Duration(milliseconds: 500));
    expect(tester.testTextInput.isVisible, isTrue);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('xterm: the cursor is translucent, so the character under it shows', (tester) async {
    await _pump(tester, _FakeTerminalRepo());
    final cursor = tester.widget<TerminalView>(find.byType(TerminalView)).theme.cursor;
    expect(cursor.a, greaterThan(0));
    expect(cursor.a, lessThan(1));
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('both emulators start at the font size from Settings', (tester) async {
    for (final e in TerminalEmulator.values) {
      await tester.pumpWidget(ProviderScope(
        overrides: [
          terminalRepositoryProvider.overrideWithValue(_FakeTerminalRepo()),
          terminalPrefsProvider.overrideWith(() => _FixedPrefs(e, fontSize: 18)),
        ],
        child: const MaterialApp(
          home: LiveScreenScreen(terminal: NodeTerminal(id: 'A:@1', command: 'zsh')),
        ),
      ));
      await tester.pump();
      final size = e == TerminalEmulator.xterm
          ? tester.widget<TerminalView>(find.byType(TerminalView)).textStyle.fontSize
          : tester.widget<gt.TerminalView>(find.byType(gt.TerminalView)).theme!.fontSize;
      expect(size, 18, reason: e.name);
      await tester.pumpWidget(const SizedBox());
    }
  });
}

class _FixedPrefs extends TerminalPrefsController {
  _FixedPrefs(this.emulator, {this.fontSize = 12});
  final TerminalEmulator emulator;
  final double fontSize;
  @override
  TerminalPrefs build() => TerminalPrefs(emulator: emulator, fontSize: fontSize);
}

class _CountingTerminals extends TerminalsNotifier {
  int loads = 0;
  @override
  Future<void> load(GatewayClient? client) async => loads++;
}
class _FixedTerminals extends TerminalsNotifier {
  _FixedTerminals(this.terminals);
  final List<NodeTerminal> terminals;
  @override
  TerminalsState build() => TerminalsState(terminals: terminals, loaded: true);
}
