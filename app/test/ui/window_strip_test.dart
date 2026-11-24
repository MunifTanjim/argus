import 'package:flutter/foundation.dart';
import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/ui/theme.dart';
import 'package:argus/ui/window_strip.dart';

Widget _app(Widget child) => MaterialApp(
      builder: (context, navigator) => WindowStrip(child: navigator!),
      home: child,
    );

void main() {
  final stripFinder = find.byKey(const ValueKey('window-strip'));

  for (final platform in [
    TargetPlatform.linux,
    TargetPlatform.android,
    TargetPlatform.iOS,
  ]) {
    testWidgets('no strip on $platform', (tester) async {
      debugDefaultTargetPlatformOverride = platform;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
      await tester.pumpWidget(_app(const SizedBox.expand(key: Key('x'))));
      expect(stripFinder, findsNothing);
      expect(tester.getTopLeft(find.byKey(const Key('x'))), Offset.zero);
      expect(tester.getSize(find.byKey(const Key('x'))),
          tester.view.physicalSize / tester.view.devicePixelRatio);
    });
  }

  testWidgets('macOS strip is 28 px above the child', (tester) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);
    await tester.pumpWidget(_app(const SizedBox.expand(key: Key('x'))));
    expect(tester.getSize(stripFinder).height, 28);
    expect(tester.getTopLeft(stripFinder), Offset.zero);
    expect(tester.getTopLeft(find.byKey(const Key('x'))), const Offset(0, 28));
    final box = tester.widget<ColoredBox>(
      find.descendant(of: stripFinder, matching: find.byType(ColoredBox)),
    );
    expect(box.color, AppColors.canvas);
  });

  testWidgets('drag on the strip calls startDrag; double-tap calls doubleClick',
      (tester) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);
    final calls = <String>[];
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      windowChannel,
      (call) async {
        calls.add(call.method);
        return null;
      },
    );
    addTearDown(() => tester.binding.defaultBinaryMessenger
        .setMockMethodCallHandler(windowChannel, null));
    await tester.pumpWidget(_app(const SizedBox.expand()));

    final mouse = await tester.startGesture(const Offset(100, 10),
        kind: PointerDeviceKind.mouse);
    await mouse.moveBy(const Offset(40, 0));
    await mouse.up();
    expect(calls, ['startDrag']);

    calls.clear();
    final pad = await tester.createGesture(kind: PointerDeviceKind.trackpad);
    await pad.panZoomStart(const Offset(100, 10));
    await pad.panZoomUpdate(const Offset(100, 10), pan: const Offset(40, 0));
    await pad.panZoomEnd();
    await tester.pump();
    expect(calls, isEmpty);

    calls.clear();
    await tester.tapAt(const Offset(100, 10), kind: PointerDeviceKind.mouse);
    await tester.pump(const Duration(milliseconds: 50));
    await tester.tapAt(const Offset(100, 10), kind: PointerDeviceKind.mouse);
    await tester.pumpAndSettle();
    expect(calls, ['doubleClick']);
  });

  testWidgets('the strip collapses in full screen and returns after',
      (tester) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);
    await tester.pumpWidget(_app(const SizedBox.expand(key: Key('x'))));
    final x = find.byKey(const Key('x'));

    Future<void> fullScreen(bool on) async {
      await tester.binding.defaultBinaryMessenger.handlePlatformMessage(
        windowChannel.name,
        windowChannel.codec.encodeMethodCall(MethodCall('fullScreen', on)),
        (_) {},
      );
      await tester.pump();
    }

    await fullScreen(true);
    expect(tester.getTopLeft(x), Offset.zero);
    expect(tester.getSize(stripFinder).height, 0);

    await fullScreen(false);
    expect(tester.getTopLeft(x), const Offset(0, 28));
    expect(tester.getSize(stripFinder).height, 28);
  });

  testWidgets('a full-window pushed route keeps the strip on top',
      (tester) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);
    await tester.pumpWidget(_app(const SizedBox.expand()));
    final nav = tester.state<NavigatorState>(find.byType(Navigator));
    nav.push(MaterialPageRoute<void>(
      builder: (_) => Scaffold(
        appBar: AppBar(title: const Text('pushed')),
        body: const SizedBox.expand(),
      ),
    ));
    await tester.pumpAndSettle();
    expect(tester.getTopLeft(find.byType(AppBar)).dy, 28);
    expect(tester.getTopLeft(stripFinder).dy, 0);
  });

  group('sidebar', () {
    final tint = find.byKey(const ValueKey('window-strip-sidebar'));

    // Returns the owner route.
    Future<Route<dynamic>> pumpOwner(
        WidgetTester tester, SidebarStripController strip) async {
      late Route<dynamic> owner;
      debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
      await tester.pumpWidget(MaterialApp(
        navigatorObservers: [strip],
        builder: (context, navigator) =>
            WindowStrip(controller: strip, child: navigator!),
        home: Builder(builder: (context) {
          owner = ModalRoute.of(context)!;
          WindowStrip.sidebarOf(context)!.show(owner, 300);
          return const SizedBox.expand();
        }),
      ));
      await tester.pump();
      return owner;
    }

    testWidgets('the strip is tinted over the owner route\'s sidebar',
        (tester) async {
      final strip = SidebarStripController();
      addTearDown(strip.dispose);
      await pumpOwner(tester, strip);
      expect(tester.getRect(tint), const Rect.fromLTWH(0, 0, 300, 28));
      expect(tester.widget<ColoredBox>(tint).color, kSidebarTint);
      final rest = find.descendant(
        of: stripFinder,
        matching: find.byWidgetPredicate(
            (w) => w is ColoredBox && w.color == AppColors.canvas),
      );
      expect(tester.getRect(rest).left, 300);
    });

    testWidgets('an opaque route over the owner makes the strip canvas',
        (tester) async {
      final strip = SidebarStripController();
      addTearDown(strip.dispose);
      await pumpOwner(tester, strip);
      final nav = tester.state<NavigatorState>(find.byType(Navigator));
      nav.push(MaterialPageRoute<void>(
          fullscreenDialog: true, builder: (_) => const SizedBox.expand()));
      await tester.pumpAndSettle();
      expect(tint, findsNothing);
      nav.pop();
      await tester.pumpAndSettle();
      expect(tint, findsOneWidget);
    });

    testWidgets('a dialog over the owner keeps the tint', (tester) async {
      final strip = SidebarStripController();
      addTearDown(strip.dispose);
      await pumpOwner(tester, strip);
      showDialog<void>(
          context: tester.state<NavigatorState>(find.byType(Navigator)).context,
          builder: (_) => const Text('dialog'));
      await tester.pumpAndSettle();
      expect(tint, findsOneWidget);
    });

    testWidgets('hide by the owner clears the tint', (tester) async {
      final strip = SidebarStripController();
      addTearDown(strip.dispose);
      final owner = await pumpOwner(tester, strip);
      strip.hide(MaterialPageRoute<void>(builder: (_) => const SizedBox()));
      await tester.pump();
      expect(tint, findsOneWidget);
      strip.hide(owner);
      await tester.pump();
      expect(tint, findsNothing);
    });
  });
}
