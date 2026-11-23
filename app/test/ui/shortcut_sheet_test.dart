import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/projects.dart';
import 'package:argus/ui/home_shell.dart';
import 'package:argus/ui/shortcut_sheet.dart';

import '../support/fake_gateway_client.dart';

void main() {
  void platform(TargetPlatform p) {
    debugDefaultTargetPlatformOverride = p;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);
  }

  Future<void> pumpShell(WidgetTester tester) async {
    tester.view.physicalSize = const Size(1200, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final c = ProviderContainer(
      overrides: [gatewayProvider.overrideWithValue(null)],
    );
    addTearDown(c.dispose);
    await c
        .read(projectsProvider.notifier)
        .load(FakeGatewayClient((m, p) async => {'projects': []}));
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: c,
        child: const MaterialApp(home: HomeShell()),
      ),
    );
    await tester.pump();
  }

  Future<void> pumpSheet(WidgetTester tester) async {
    late BuildContext ctx;
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) {
            ctx = context;
            return const Scaffold();
          },
        ),
      ),
    );
    showShortcutSheet(ctx);
    await tester.pumpAndSettle();
  }

  testWidgets('? outside a text field opens the shortcut sheet', (
    tester,
  ) async {
    platform(TargetPlatform.macOS);
    await pumpShell(tester);
    await tester.sendKeyDownEvent(LogicalKeyboardKey.shiftLeft);
    await tester.sendKeyDownEvent(LogicalKeyboardKey.slash, character: '?');
    await tester.sendKeyUpEvent(LogicalKeyboardKey.slash);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.shiftLeft);
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('shortcut-sheet')), findsOneWidget);
  });

  testWidgets('the sheet shows ⌘K on macOS and Ctrl+K on linux', (
    tester,
  ) async {
    platform(TargetPlatform.macOS);
    await pumpSheet(tester);
    expect(find.text('⌘K'), findsOneWidget);
    expect(find.text('Ctrl+K'), findsNothing);
    expect(find.text('Enter'), findsOneWidget);
    expect(find.text('Send reply'), findsOneWidget);
    expect(find.text('Shift+Enter'), findsOneWidget);
    expect(find.text('New line'), findsOneWidget);

    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pumpAndSettle();
    platform(TargetPlatform.linux);
    await pumpSheet(tester);
    expect(find.text('Ctrl+K'), findsOneWidget);
    expect(find.text('⌘K'), findsNothing);
  });

  testWidgets('Esc closes the sheet', (tester) async {
    platform(TargetPlatform.macOS);
    await pumpSheet(tester);
    expect(find.byKey(const Key('shortcut-sheet')), findsOneWidget);
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('shortcut-sheet')), findsNothing);
  });

  testWidgets('showShortcutSheet while open does not stack another', (
    tester,
  ) async {
    platform(TargetPlatform.macOS);
    await pumpSheet(tester);
    showShortcutSheet(tester.element(find.byKey(const Key('shortcut-sheet'))));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('shortcut-sheet')), findsOneWidget);
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('shortcut-sheet')), findsNothing);
  });
}
