import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/ui/desktop_shortcuts.dart';
import 'package:argus/ui/mac_menu_bar.dart';

void _platform(TargetPlatform p) {
  debugDefaultTargetPlatformOverride = p;
  addTearDown(() => debugDefaultTargetPlatformOverride = null);
}

// The test binding has no menu plugin; count what would be sent to it.
List<MethodCall> _menuChannel(WidgetTester tester) {
  final calls = <MethodCall>[];
  final messenger = tester.binding.defaultBinaryMessenger;
  messenger.setMockMethodCallHandler(SystemChannels.menu, (call) async {
    calls.add(call);
    return null;
  });
  addTearDown(
      () => messenger.setMockMethodCallHandler(SystemChannels.menu, null));
  return calls;
}

ShellMenuActions _actions(List<String> log) => ShellMenuActions(
      onBack: () => log.add('back'),
      onSpawn: () => log.add('spawn'),
      onSettings: () => log.add('settings'),
      onQuickSwitcher: () => log.add('quick'),
      onShortcutSheet: () => log.add('sheet'),
      onSelectTab: (i) => log.add('tab $i'),
    );

ShellMenuRegistry _registry() {
  final registry = ShellMenuRegistry();
  addTearDown(registry.dispose);
  return registry;
}

// With [log], a shell is registered whose actions add to it.
MacMenuBar _bar(List<String>? log,
    {Widget child = const Text('body'), ShellMenuRegistry? registry}) {
  registry ??= _registry();
  if (log != null) registry.register(_actions(log));
  return MacMenuBar(registry: registry, child: child);
}

Future<List<String>> _pump(WidgetTester tester, {Widget? child}) async {
  final log = <String>[];
  await tester.pumpWidget(MaterialApp(
    home: Scaffold(body: child == null ? _bar(log) : _bar(log, child: child)),
  ));
  return log;
}

const _appItems = [
  ('Argus', 'Settings…'),
  ('View', 'Quick Switcher'),
  ('View', 'Sessions'),
  ('View', 'History'),
  ('View', 'Terminals'),
  ('View', 'Keyboard Shortcuts'),
  ('Session', 'New Session'),
  ('Session', 'Back'),
];

List<PlatformMenuItem> _menus(WidgetTester tester) =>
    tester.widget<PlatformMenuBar>(find.byType(PlatformMenuBar)).menus;

Iterable<PlatformMenuItem> _all(List<PlatformMenuItem> items) sync* {
  for (final item in items) {
    yield item;
    if (item is PlatformMenu) yield* _all(item.menus);
    if (item is PlatformMenuItemGroup) yield* _all(item.members);
  }
}

PlatformMenu _menu(WidgetTester tester, String label) =>
    _menus(tester).whereType<PlatformMenu>().singleWhere((m) => m.label == label);

PlatformMenuItem _item(WidgetTester tester, String menu, String label) =>
    _all(_menu(tester, menu).menus).singleWhere(
        (i) => i is! PlatformMenuItemGroup && i.label == label);

List<PlatformProvidedMenuItemType> _provided(PlatformMenu menu) => [
      for (final i in _all(menu.menus))
        if (i is PlatformProvidedMenuItem) i.type,
    ];

LogicalKeyboardKey get _cmd => LogicalKeyboardKey.metaLeft;

Future<void> _chord(WidgetTester tester, LogicalKeyboardKey key) async {
  await tester.sendKeyDownEvent(_cmd);
  await tester.sendKeyDownEvent(key);
  await tester.sendKeyUpEvent(key);
  await tester.sendKeyUpEvent(_cmd);
  await tester.pump();
}

void main() {
  testWidgets(
      'macOS: PlatformMenuBar is present with Argus, Edit, View, Session, Window menus',
      (tester) async {
    _platform(TargetPlatform.macOS);
    final sent = _menuChannel(tester);
    await _pump(tester);
    expect(find.text('body'), findsOneWidget);
    expect([for (final m in _menus(tester)) m.label],
        ['Argus', 'Edit', 'View', 'Session', 'Window']);
    expect(_provided(_menu(tester, 'Argus')), [
      PlatformProvidedMenuItemType.about,
      PlatformProvidedMenuItemType.hide,
      PlatformProvidedMenuItemType.hideOtherApplications,
      PlatformProvidedMenuItemType.showAllApplications,
      PlatformProvidedMenuItemType.quit,
    ]);
    expect(_item(tester, 'Argus', 'Settings…'), isNotNull);
    for (final label in ['Cut', 'Copy', 'Paste', 'Select All']) {
      expect(_item(tester, 'Edit', label), isNotNull);
    }
    for (final label in [
      'Quick Switcher',
      'Sessions',
      'History',
      'Terminals',
      'Keyboard Shortcuts',
    ]) {
      expect(_item(tester, 'View', label), isNotNull);
    }
    expect(_item(tester, 'Session', 'New Session'), isNotNull);
    expect(_item(tester, 'Session', 'Back'), isNotNull);
    expect(_provided(_menu(tester, 'Window')), [
      PlatformProvidedMenuItemType.minimizeWindow,
      PlatformProvidedMenuItemType.zoomWindow,
      PlatformProvidedMenuItemType.toggleFullScreen,
    ]);
    expect(sent.where((c) => c.method == 'Menu.setMenus'), isNotEmpty);
  });

  for (final p in [TargetPlatform.linux, TargetPlatform.android]) {
    testWidgets('${p.name}: no PlatformMenuBar', (tester) async {
      _platform(p);
      await _pump(tester);
      expect(find.byType(PlatformMenuBar), findsNothing);
      expect(find.text('body'), findsOneWidget);
    });
  }

  testWidgets('selecting View › History calls onSelectTab(1)', (tester) async {
    _platform(TargetPlatform.macOS);
    _menuChannel(tester);
    final log = await _pump(tester);
    _item(tester, 'View', 'Sessions').onSelected!();
    _item(tester, 'View', 'History').onSelected!();
    _item(tester, 'View', 'Terminals').onSelected!();
    expect(log, ['tab 0', 'tab 1', 'tab 2']);
  });

  testWidgets('Session › New Session calls onSpawn', (tester) async {
    _platform(TargetPlatform.macOS);
    _menuChannel(tester);
    final log = await _pump(tester);
    _item(tester, 'Session', 'New Session').onSelected!();
    expect(log, ['spawn']);
  });

  testWidgets('the other custom items call their actions', (tester) async {
    _platform(TargetPlatform.macOS);
    _menuChannel(tester);
    final log = await _pump(tester);
    _item(tester, 'Argus', 'Settings…').onSelected!();
    _item(tester, 'View', 'Quick Switcher').onSelected!();
    _item(tester, 'View', 'Keyboard Shortcuts').onSelected!();
    _item(tester, 'Session', 'Back').onSelected!();
    expect(log, ['settings', 'quick', 'sheet', 'back']);
  });

  // Flutter handles a key first, and AppKit redispatches the keys it leaves
  // unhandled to the main menu. An app item with a key equivalent would then
  // fire whenever DesktopShortcuts' guards leave the key alone.
  testWidgets('no custom item has a key equivalent', (tester) async {
    _platform(TargetPlatform.macOS);
    _menuChannel(tester);
    await _pump(tester);
    final custom = [
      for (final i in _all(_menus(tester)))
        if (i is! PlatformMenu &&
            i is! PlatformMenuItemGroup &&
            i is! PlatformProvidedMenuItem)
          i,
    ];
    expect(custom, hasLength(12));
    for (final i in custom) {
      expect(i.shortcut, isNull, reason: i.label);
    }
  });

  testWidgets('Edit › Select All selects the text of the focused field',
      (tester) async {
    _platform(TargetPlatform.macOS);
    _menuChannel(tester);
    final text = TextEditingController(text: 'hello');
    addTearDown(text.dispose);
    await _pump(tester, child: TextField(controller: text, autofocus: true));
    await tester.pump();
    text.selection = const TextSelection.collapsed(offset: 0);
    _item(tester, 'Edit', 'Select All').onSelected!();
    await tester.pump();
    expect(text.selection,
        const TextSelection(baseOffset: 0, extentOffset: 5));
  });

  testWidgets(
      'rebuilds and a new shell do not send the menus again; the new shell is called',
      (tester) async {
    _platform(TargetPlatform.macOS);
    final sent = _menuChannel(tester);
    final registry = _registry();
    final first = _actions(<String>[]);
    registry.register(first);
    Future<void> pump() => tester.pumpWidget(MaterialApp(
        home: Scaffold(body: _bar(null, registry: registry))));
    await pump();
    // PlatformMenuBar sends once more on its first update, whatever the items.
    await pump();
    final before = sent.length;
    await pump();
    expect(sent.length, before);
    final log = <String>[];
    // As when a widget swap mounts a new shell before the old one is disposed.
    registry.register(_actions(log));
    registry.unregister(first);
    await tester.pump();
    await pump();
    expect(sent.length, before);
    _item(tester, 'Session', 'New Session').onSelected!();
    expect(log, ['spawn']);
  });

  testWidgets(
      'with no shell, the app items are disabled and the provided and Edit items stay',
      (tester) async {
    _platform(TargetPlatform.macOS);
    _menuChannel(tester);
    final registry = _registry();
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(body: _bar(null, registry: registry))));
    expect(find.byType(PlatformMenuBar), findsOneWidget);
    expect(_provided(_menu(tester, 'Argus')),
        contains(PlatformProvidedMenuItemType.quit));
    for (final (menu, label) in _appItems) {
      expect(_item(tester, menu, label).onSelected, isNull, reason: label);
    }
    for (final label in ['Cut', 'Copy', 'Paste', 'Select All']) {
      expect(_item(tester, 'Edit', label).onSelected, isNotNull);
    }

    final log = <String>[];
    final actions = _actions(log);
    registry.register(actions);
    await tester.pump();
    for (final (menu, label) in _appItems) {
      _item(tester, menu, label).onSelected!();
    }
    expect(log, [
      'settings', 'quick', 'tab 0', 'tab 1', 'tab 2', 'sheet', 'spawn', 'back',
    ]);

    registry.unregister(actions);
    await tester.pump();
    expect(_item(tester, 'Session', 'Back').onSelected, isNull);
  });

  testWidgets('unregistering an old shell keeps the current one',
      (tester) async {
    final registry = _registry();
    final old = _actions(<String>[]);
    final current = _actions(<String>[]);
    registry.register(old);
    registry.register(current);
    registry.unregister(old);
    expect(registry.actions, same(current));
  });

  testWidgets(
      '⌘K over a focused live terminal still does not open the switcher; ⌘N fires once',
      (tester) async {
    _platform(TargetPlatform.macOS);
    _menuChannel(tester);
    final seen = <LogicalKeyboardKey>[];
    final terminal = FocusNode(onKeyEvent: (_, e) {
      if (e is KeyDownEvent) seen.add(e.logicalKey);
      return KeyEventResult.handled;
    });
    addTearDown(terminal.dispose);
    liveTerminalFocusNodes.add(terminal);
    addTearDown(() => liveTerminalFocusNodes.remove(terminal));
    final log = <String>[];
    // As in the app: the menu bar around the shortcuts.
    await tester.pumpWidget(MaterialApp(
      home: _bar(
        log,
        child: DesktopShortcuts(
          onBack: () => log.add('back'),
          onSpawn: () => log.add('spawn'),
          onSettings: () => log.add('settings'),
          onQuickSwitcher: () => log.add('quick'),
          onShortcutSheet: () => log.add('sheet'),
          child: Focus(focusNode: terminal, child: const Text('terminal')),
        ),
      ),
    ));
    terminal.requestFocus();
    await tester.pump();
    await _chord(tester, LogicalKeyboardKey.keyK);
    await _chord(tester, LogicalKeyboardKey.keyN);
    expect(log, ['spawn']);
    expect(seen, contains(LogicalKeyboardKey.keyK));
    expect(seen, isNot(contains(LogicalKeyboardKey.keyN)));
  });
}
