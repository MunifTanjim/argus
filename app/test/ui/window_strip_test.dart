import 'package:flutter/foundation.dart';
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
    expect(kMacTitleStripHeight, 28);
    expect(tester.getSize(stripFinder).height, 28);
    expect(tester.getTopLeft(stripFinder), Offset.zero);
    expect(tester.getTopLeft(find.byKey(const Key('x'))), const Offset(0, 28));
    final box = tester.widget<ColoredBox>(
      find.descendant(of: stripFinder, matching: find.byType(ColoredBox)),
    );
    expect(box.color, kWindowStripColor);
    expect(kWindowStripColor, AppColors.canvas);
  });

  testWidgets('drag on the strip calls startDrag; double-tap calls zoom',
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

    await tester.dragFrom(const Offset(100, 10), const Offset(40, 0));
    expect(calls, ['startDrag']);

    calls.clear();
    await tester.tapAt(const Offset(100, 10));
    await tester.pump(const Duration(milliseconds: 50));
    await tester.tapAt(const Offset(100, 10));
    await tester.pumpAndSettle();
    expect(calls, ['zoom']);
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
}
