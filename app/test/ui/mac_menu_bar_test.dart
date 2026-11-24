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

MacMenuBar _bar(List<String> log, {Widget child = const Text('body')}) =>
    MacMenuBar(
      onBack: () => log.add('back'),
      onSpawn: () => log.add('spawn'),
      onSettings: () => log.add('settings'),
      onQuickSwitcher: () => log.add('quick'),
      onShortcutSheet: () => log.add('sheet'),
      onSelectTab: (i) => log.add('tab $i'),
      child: child,
    );

Future<List<String>> _pump(WidgetTester tester, {Widget? child}) async {
  final log = <String>[];
  await tester.pumpWidget(MaterialApp(
    home: Scaffold(body: child == null ? _bar(log) : _bar(log, child: child)),
  ));
  return log;
}

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
      'macOS: PlatformMenuBar is present with argus, Edit, View, Session, Window menus',
      (tester) async {
    _platform(TargetPlatform.macOS);
    final sent = _menuChannel(tester);
    await _pump(tester);
    expect(find.text('body'), findsOneWidget);
    expect([for (final m in _menus(tester)) m.label],
        ['argus', 'Edit', 'View', 'Session', 'Window']);
    expect(_provided(_menu(tester, 'argus')), [
      PlatformProvidedMenuItemType.about,
      PlatformProvidedMenuItemType.hide,
      PlatformProvidedMenuItemType.quit,
    ]);
    expect(_item(tester, 'argus', 'Settings…'), isNotNull);
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
    _item(tester, 'argus', 'Settings…').onSelected!();
    _item(tester, 'View', 'Quick Switcher').onSelected!();
    _item(tester, 'View', 'Keyboard Shortcuts').onSelected!();
    _item(tester, 'Session', 'Back').onSelected!();
    expect(log, ['settings', 'quick', 'sheet', 'back']);
  });

  // An NSMenuItem key equivalent fires before Flutter sees the key, which
  // would skip DesktopShortcuts' text field and terminal guards.
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

  testWidgets('a rebuild with new callbacks does not send the menus again',
      (tester) async {
    _platform(TargetPlatform.macOS);
    final sent = _menuChannel(tester);
    final log = await _pump(tester);
    // PlatformMenuBar sends once more on its first update, whatever the items.
    await _pump(tester);
    final before = sent.length;
    await _pump(tester);
    expect(sent.length, before);
    _item(tester, 'Session', 'New Session').onSelected!();
    expect(log, isEmpty, reason: 'the first callbacks are replaced');
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
    // As in HomeShell: the menu bar around the shortcuts.
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
