import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/enums.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/navigation.dart';
import 'package:argus/state/projects.dart';
import 'package:argus/state/session_filter.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/state/terminal_prefs.dart';
import 'package:argus/ui/desktop_shortcuts.dart';
import 'package:argus/ui/home_shell.dart';
import 'package:argus/ui/live_emulator.dart';
import 'package:argus/ui/session_card.dart';
import 'package:argus/ui/session_composer.dart';
import 'package:argus/ui/session_detail_screen.dart';
import 'package:argus/ui/session_list_screen.dart';

import '../support/fake_gateway_client.dart';

class _Calls {
  final log = <String>[];
}

void _platform(TargetPlatform p) {
  debugDefaultTargetPlatformOverride = p;
  addTearDown(() => debugDefaultTargetPlatformOverride = null);
}

// The shortcuts above a nested navigator, as in HomeShell, so popup routes
// open under them.
Future<_Calls> _pump(WidgetTester tester, Widget body) async {
  final calls = _Calls();
  await tester.pumpWidget(MaterialApp(
    home: DesktopShortcuts(
      onBack: () => calls.log.add('back'),
      onSpawn: () => calls.log.add('spawn'),
      onSettings: () => calls.log.add('settings'),
      onQuickSwitcher: () => calls.log.add('quick'),
      onShortcutSheet: () => calls.log.add('sheet'),
      child: Navigator(
        onGenerateRoute: (s) => MaterialPageRoute(
          settings: s,
          builder: (_) => Scaffold(body: body),
        ),
      ),
    ),
  ));
  await tester.pump();
  return calls;
}

LogicalKeyboardKey get _mod =>
    defaultTargetPlatform == TargetPlatform.macOS
        ? LogicalKeyboardKey.metaLeft
        : LogicalKeyboardKey.controlLeft;

Future<bool> _chord(WidgetTester tester, LogicalKeyboardKey key) async {
  await tester.sendKeyDownEvent(_mod);
  final handled = await tester.sendKeyDownEvent(key);
  await tester.sendKeyUpEvent(key);
  await tester.sendKeyUpEvent(_mod);
  await tester.pump();
  return handled;
}

Future<bool> _press(WidgetTester tester, LogicalKeyboardKey key,
    {String? character, bool shift = false}) async {
  if (shift) await tester.sendKeyDownEvent(LogicalKeyboardKey.shiftLeft);
  final handled = await tester.sendKeyDownEvent(key, character: character);
  await tester.sendKeyUpEvent(key);
  if (shift) await tester.sendKeyUpEvent(LogicalKeyboardKey.shiftLeft);
  await tester.pump();
  return handled;
}

// The engine hands a key to the text input only if no key handler took it.
Future<void> _typeInto(
    WidgetTester tester, TextEditingController text, String chars) async {
  for (final ch in chars.split('')) {
    final (key, shift) = switch (ch) {
      '/' => (LogicalKeyboardKey.slash, false),
      '?' => (LogicalKeyboardKey.slash, true),
      'j' => (LogicalKeyboardKey.keyJ, false),
      'k' => (LogicalKeyboardKey.keyK, false),
      _ => throw ArgumentError(ch),
    };
    final handled = await _press(tester, key, character: ch, shift: shift);
    expect(handled, isFalse, reason: 'a shortcut took "$ch"');
    tester.testTextInput.enterText(text.text + ch);
    await tester.pump();
  }
}

Session _session(String id) => Session(
      id: id,
      agent: 'claude',
      tmux: const TmuxLocation(
        server: TmuxServerKind.default_,
        paneId: '',
        sessionName: '',
        windowIndex: 0,
        currentPath: '',
      ),
      status: SessionStatus.awaitingInput,
      source: SessionSource.discovered,
      inputMode: InputMode.pane,
    );

Future<ProviderContainer> _shell(WidgetTester tester) async {
  _platform(TargetPlatform.macOS);
  tester.view.physicalSize = const Size(1200, 800);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final c = ProviderContainer(overrides: [
    gatewayProvider.overrideWithValue(null),
  ]);
  addTearDown(c.dispose);
  await c
      .read(projectsProvider.notifier)
      .load(FakeGatewayClient((m, p) async => {'projects': []}));
  await tester.pumpWidget(UncontrolledProviderScope(
    container: c,
    child: const MaterialApp(home: HomeShell()),
  ));
  await tester.pump();
  return c;
}

// SessionDetailScreen spins forever without a connection.
Future<void> _pumpRoute(WidgetTester tester) async {
  await tester.pump();
  await tester.pump(const Duration(seconds: 1));
}

Future<void> _openSession(WidgetTester tester, ProviderContainer c) async {
  c.read(sessionsProvider.notifier).replaceAll([_session('s1')]);
  await tester.pump();
  await tester.tap(find.byType(SessionCard));
  await _pumpRoute(tester);
  expect(find.byType(SessionDetailScreen), findsOneWidget);
}

EditableText _composerField(WidgetTester tester) => tester.widget<EditableText>(
      find.descendant(
          of: find.byType(SessionComposer), matching: find.byType(EditableText)),
    );

void main() {
  testWidgets('Cmd+N on macOS calls onSpawn', (tester) async {
    _platform(TargetPlatform.macOS);
    final calls = await _pump(tester, const Text('body'));
    expect(await _chord(tester, LogicalKeyboardKey.keyN), isTrue);
    expect(calls.log, ['spawn']);
  });

  testWidgets('Ctrl+N on linux calls onSpawn', (tester) async {
    _platform(TargetPlatform.linux);
    final calls = await _pump(tester, const Text('body'));
    await _chord(tester, LogicalKeyboardKey.keyN);
    expect(calls.log, ['spawn']);
  });

  testWidgets('Cmd+K, Cmd+, and ? call their handlers', (tester) async {
    _platform(TargetPlatform.macOS);
    final calls = await _pump(tester, const Text('body'));
    await _chord(tester, LogicalKeyboardKey.keyK);
    await _chord(tester, LogicalKeyboardKey.comma);
    await _press(tester, LogicalKeyboardKey.slash, character: '?', shift: true);
    expect(calls.log, ['quick', 'settings', 'sheet']);
  });

  testWidgets('/ does not open the shortcut sheet', (tester) async {
    _platform(TargetPlatform.macOS);
    final calls = await _pump(tester, const Text('body'));
    await _press(tester, LogicalKeyboardKey.slash, character: '/');
    expect(calls.log, isEmpty);
  });

  testWidgets('Cmd+2 selects the History tab', (tester) async {
    final c = await _shell(tester);
    expect(c.read(homeTabProvider), HomeTab.sessions);
    await _chord(tester, LogicalKeyboardKey.digit2);
    await tester.pump();
    expect(c.read(homeTabProvider), HomeTab.history);
  });

  testWidgets('Cmd+[ goes back from an open session', (tester) async {
    final c = await _shell(tester);
    await _openSession(tester, c);
    await _chord(tester, LogicalKeyboardKey.bracketLeft);
    await _pumpRoute(tester);
    expect(find.byType(SessionDetailScreen), findsNothing);
  });

  testWidgets('Cmd+L focuses the composer of the open session', (tester) async {
    final c = await _shell(tester);
    await _openSession(tester, c);
    expect(_composerField(tester).focusNode.hasFocus, isFalse);
    await _chord(tester, LogicalKeyboardKey.keyL);
    expect(_composerField(tester).focusNode.hasFocus, isTrue);
  });

  testWidgets('/ outside a text field focuses the composer', (tester) async {
    final c = await _shell(tester);
    await _openSession(tester, c);
    expect(_composerField(tester).focusNode.hasFocus, isFalse);
    expect(await _press(tester, LogicalKeyboardKey.slash, character: '/'),
        isTrue);
    expect(_composerField(tester).focusNode.hasFocus, isTrue);
  });

  testWidgets('typing / j k ? in the composer inserts the characters',
      (tester) async {
    final c = await _shell(tester);
    await _openSession(tester, c);
    await _chord(tester, LogicalKeyboardKey.keyL);
    final field = _composerField(tester);
    await _typeInto(tester, field.controller, '/jk?');
    expect(_composerField(tester).controller.text, '/jk?');
    expect(_composerField(tester).focusNode.hasFocus, isTrue);
    expect(find.byType(SessionDetailScreen), findsOneWidget);
  });

  testWidgets('typing in the session search field inserts the characters',
      (tester) async {
    final c = await _shell(tester);
    c.read(sessionSearchProvider.notifier).open();
    await tester.pump();
    final finder = find.descendant(
        of: find.byType(SessionListScreen), matching: find.byType(EditableText));
    final field = tester.widget<EditableText>(finder);
    expect(field.focusNode.hasFocus, isTrue);
    await _typeInto(tester, field.controller, '/jk?');
    expect(field.controller.text, '/jk?');
    expect(field.focusNode.hasFocus, isTrue);
  });

  testWidgets(
      'on linux, Ctrl+K Ctrl+L Ctrl+[ with the live terminal focused call no handler',
      (tester) async {
    _platform(TargetPlatform.linux);
    final terminal = FocusNode();
    addTearDown(terminal.dispose);
    liveTerminalFocusNodes.add(terminal);
    addTearDown(() => liveTerminalFocusNodes.remove(terminal));
    final composer = FocusNode();
    addTearDown(composer.dispose);
    activeComposerFocus.value = composer;
    addTearDown(() => activeComposerFocus.value = null);
    final calls = await _pump(
      tester,
      Column(children: [
        Focus(focusNode: terminal, child: const Text('terminal')),
        Focus(focusNode: composer, child: const Text('composer')),
      ]),
    );
    terminal.requestFocus();
    await tester.pump();
    for (final key in [
      LogicalKeyboardKey.keyK,
      LogicalKeyboardKey.keyL,
      LogicalKeyboardKey.bracketLeft,
      LogicalKeyboardKey.keyN,
    ]) {
      expect(await _chord(tester, key), isFalse);
    }
    expect(calls.log, isEmpty);
    expect(terminal.hasFocus, isTrue);
  });

  testWidgets(
      'on macOS, Mod shortcuts other than Cmd+K fire over the real ghostty terminal',
      (tester) async {
    _platform(TargetPlatform.macOS);
    final emulator = LiveEmulator(
      TerminalEmulator.ghostty,
      direct: true,
      sink: LiveSink(
        send: (_) {},
        resize: (_, _) {},
        typed: utf8.encode,
        pinching: () => false,
      ),
    );
    addTearDown(emulator.dispose);
    final calls = await _pump(tester, emulator.view(12));
    liveTerminalFocusNodes.single.requestFocus();
    await tester.pump();
    expect(liveTerminalHasFocus(), isTrue);
    await _chord(tester, LogicalKeyboardKey.keyK);
    expect(calls.log, isEmpty, reason: 'Cmd+K clears the terminal');
    await _chord(tester, LogicalKeyboardKey.keyN);
    await _chord(tester, LogicalKeyboardKey.comma);
    await _chord(tester, LogicalKeyboardKey.bracketLeft);
    expect(calls.log, ['spawn', 'settings', 'back']);
    await _press(tester, LogicalKeyboardKey.escape);
    await _press(tester, LogicalKeyboardKey.slash, character: '?', shift: true);
    expect(calls.log, ['spawn', 'settings', 'back']);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets(
      'on macOS, Cmd+N fires over a terminal that consumes every key; Cmd+K does not',
      (tester) async {
    _platform(TargetPlatform.macOS);
    final seen = <LogicalKeyboardKey>[];
    final terminal = FocusNode(onKeyEvent: (_, e) {
      seen.add(e.logicalKey);
      return KeyEventResult.handled;
    });
    addTearDown(terminal.dispose);
    liveTerminalFocusNodes.add(terminal);
    addTearDown(() => liveTerminalFocusNodes.remove(terminal));
    final calls = await _pump(
        tester, Focus(focusNode: terminal, child: const Text('terminal')));
    terminal.requestFocus();
    await tester.pump();
    await _chord(tester, LogicalKeyboardKey.keyN);
    await _chord(tester, LogicalKeyboardKey.digit1);
    await _chord(tester, LogicalKeyboardKey.keyK);
    await _press(tester, LogicalKeyboardKey.escape);
    expect(calls.log, ['spawn']);
    expect(seen, isNot(contains(LogicalKeyboardKey.keyN)),
        reason: 'neither the press nor the release of a taken key');
    expect(seen, contains(LogicalKeyboardKey.keyK));
  });

  testWidgets('J and K outside a text field move focus to the next and previous row',
      (tester) async {
    _platform(TargetPlatform.macOS);
    final rows = [FocusNode(), FocusNode(), FocusNode()];
    for (final n in rows) {
      addTearDown(n.dispose);
    }
    await _pump(
      tester,
      Column(children: [
        for (final (i, n) in rows.indexed)
          TextButton(focusNode: n, onPressed: () {}, child: Text('row $i')),
      ]),
    );
    rows[0].requestFocus();
    await tester.pump();
    expect(await _press(tester, LogicalKeyboardKey.keyJ, character: 'j'), isTrue);
    expect(rows[1].hasFocus, isTrue);
    await _press(tester, LogicalKeyboardKey.keyJ, character: 'j');
    expect(rows[2].hasFocus, isTrue);
    await _press(tester, LogicalKeyboardKey.keyK, character: 'k');
    expect(rows[1].hasFocus, isTrue);
  });

  testWidgets('/ does not focus the composer of a session under another screen',
      (tester) async {
    final c = await _shell(tester);
    await _openSession(tester, c);
    Navigator.of(tester.element(find.byType(SessionComposer))).push(
        MaterialPageRoute<void>(builder: (_) => const Scaffold(body: Text('over'))));
    await _pumpRoute(tester);
    expect(find.text('over'), findsOneWidget);
    expect(activeComposerFocus.value, isNull);
    await _press(tester, LogicalKeyboardKey.slash, character: '/');
    final composer = find.descendant(
        of: find.byType(SessionComposer, skipOffstage: false),
        matching: find.byType(EditableText, skipOffstage: false),
        skipOffstage: false);
    expect(tester.widget<EditableText>(composer).focusNode.hasFocus, isFalse);

    Navigator.of(tester.element(find.text('over'))).pop();
    await _pumpRoute(tester);
    await _press(tester, LogicalKeyboardKey.slash, character: '/');
    expect(_composerField(tester).focusNode.hasFocus, isTrue);
  });

  testWidgets('Esc with the live terminal focused does not go back',
      (tester) async {
    _platform(TargetPlatform.macOS);
    final terminal = FocusNode();
    addTearDown(terminal.dispose);
    liveTerminalFocusNodes.add(terminal);
    addTearDown(() => liveTerminalFocusNodes.remove(terminal));
    final calls = await _pump(
        tester, Focus(focusNode: terminal, child: const Text('terminal')));
    terminal.requestFocus();
    await tester.pump();
    expect(await _press(tester, LogicalKeyboardKey.escape), isFalse);
    expect(calls.log, isEmpty);
  });

  testWidgets('Esc outside a text field goes back', (tester) async {
    _platform(TargetPlatform.macOS);
    final calls = await _pump(tester, const Text('body'));
    await _press(tester, LogicalKeyboardKey.escape);
    expect(calls.log, ['back']);
  });

  testWidgets('Esc with a popup menu open closes the menu and does not go back',
      (tester) async {
    _platform(TargetPlatform.macOS);
    final calls = await _pump(
      tester,
      PopupMenuButton<int>(
        itemBuilder: (_) => const [PopupMenuItem(value: 1, child: Text('One'))],
      ),
    );
    await tester.tap(find.byType(PopupMenuButton<int>));
    await tester.pumpAndSettle();
    expect(find.text('One'), findsOneWidget);
    await _press(tester, LogicalKeyboardKey.escape);
    await tester.pumpAndSettle();
    expect(find.text('One'), findsNothing);
    expect(calls.log, isEmpty);
  });

  testWidgets('no shortcut is active on Android', (tester) async {
    final calls = await _pump(tester, const Text('body'));
    await tester.sendKeyDownEvent(LogicalKeyboardKey.controlLeft);
    await tester.sendKeyEvent(LogicalKeyboardKey.keyN);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.controlLeft);
    await _press(tester, LogicalKeyboardKey.escape);
    expect(calls.log, isEmpty);
  });

  test('shortcutDescriptions lists every binding with the platform modifier',
      () {
    debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
    final mac = shortcutDescriptions();
    debugDefaultTargetPlatformOverride = TargetPlatform.linux;
    final linux = shortcutDescriptions();
    debugDefaultTargetPlatformOverride = null;
    expect(mac, hasLength(10));
    expect(mac.first.keys, '⌘K');
    expect(mac.first.action, contains('terminal'));
    expect(linux.first.keys, 'Ctrl+K');
    expect([for (final d in mac) d.keys],
        containsAll(['/', 'Esc', 'J / K', '?']));
  });
}
