import 'package:flutter/cupertino.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:argus/ui/adaptive_nav.dart';
import 'package:argus/ui/titlebar.dart';
import 'package:macos_window_utils/macos_window_utils.dart';

const _destinations = [
  AdaptiveNavDestination(icon: Icon(Icons.forum_outlined), label: 'One'),
  AdaptiveNavDestination(icon: Icon(Icons.history), label: 'Two'),
];

Widget _scaffold(List<int> taps) => MaterialApp(
  home: AdaptiveNavScaffold(
    body: const Text('body'),
    destinations: _destinations,
    selectedIndex: 0,
    onSelected: taps.add,
  ),
);

Future<void> _pump(
  WidgetTester tester,
  TargetPlatform platform,
  double width,
  Widget app,
) async {
  debugDefaultTargetPlatformOverride = platform;
  addTearDown(() => debugDefaultTargetPlatformOverride = null);
  tester.view.physicalSize = Size(width, 700);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(app);
}

const _windowChannel = MethodChannel('macos_window_utils/window_manipulator');

void main() {
  setUpAll(() async {
    TestWidgetsFlutterBinding.ensureInitialized();
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(_windowChannel, (call) async => true);
    await WindowManipulator.initialize();
  });

  testWidgets('shows a bottom NavigationBar on iOS at 1200 px', (tester) async {
    await _pump(tester, TargetPlatform.iOS, 1200, _scaffold([]));
    expect(find.byType(NavigationBar), findsOneWidget);
    expect(find.byType(NavigationRail), findsNothing);
  });

  for (final width in [1200.0, 800.0]) {
    testWidgets(
      'macOS at $width px: no rail and no bottom bar; the tabs are a segmented control in the toolbar',
      (tester) async {
        await _pump(
          tester,
          TargetPlatform.macOS,
          width,
          MaterialApp(
            home: AdaptiveNavScaffold(
              appBar: const ArgusAppBar(),
              body: const Text('body'),
              destinations: _destinations,
              selectedIndex: 1,
              onSelected: (_) {},
            ),
          ),
        );
        expect(find.byType(NavigationRail), findsNothing);
        expect(find.byType(NavigationBar), findsNothing);
        expect(find.byType(VerticalDivider), findsNothing);
        expect(find.text('body'), findsOneWidget);
        final control = find.descendant(
          of: find.byType(ArgusAppBar),
          matching: find.byType(CupertinoSlidingSegmentedControl<int>),
        );
        expect(control, findsOneWidget);
        expect(
          tester
              .widget<CupertinoSlidingSegmentedControl<int>>(control)
              .groupValue,
          1,
        );
        for (final label in ['One', 'Two']) {
          expect(
            find.descendant(of: control, matching: find.text(label)),
            findsOneWidget,
          );
        }
      },
    );
  }

  testWidgets('macOS: a segment tap calls onSelected with the index', (
    tester,
  ) async {
    final taps = <int>[];
    await _pump(
      tester,
      TargetPlatform.macOS,
      1200,
      MaterialApp(
        home: AdaptiveNavScaffold(
          appBar: const ArgusAppBar(),
          body: const Text('body'),
          destinations: _destinations,
          selectedIndex: 0,
          onSelected: taps.add,
        ),
      ),
    );
    await tester.tap(find.text('Two'));
    await tester.pumpAndSettle();
    expect(taps, [1]);
  });

  testWidgets('macOS: the body reaches the top without a TitlebarSpacer', (
    tester,
  ) async {
    await _pump(tester, TargetPlatform.macOS, 1200, _scaffold([]));
    expect(find.byType(TitlebarSpacer), findsNothing);
    expect(tester.getTopLeft(find.text('body')).dy, 0);
  });

  testWidgets('Linux at 1200 px: a NavigationRail and no segmented control', (
    tester,
  ) async {
    await _pump(tester, TargetPlatform.linux, 1200, _scaffold([]));
    expect(find.byType(NavigationRail), findsOneWidget);
    expect(find.byType(NavigationBar), findsNothing);
    expect(find.byType(CupertinoSlidingSegmentedControl<int>), findsNothing);
  });

  testWidgets('Android: a bottom NavigationBar and no segmented control', (
    tester,
  ) async {
    await _pump(tester, TargetPlatform.android, 1200, _scaffold([]));
    expect(find.byType(NavigationBar), findsOneWidget);
    expect(find.byType(NavigationRail), findsNothing);
    expect(find.byType(CupertinoSlidingSegmentedControl<int>), findsNothing);
  });

  testWidgets('a rail tap calls onSelected with the index', (tester) async {
    final taps = <int>[];
    await _pump(tester, TargetPlatform.linux, 1200, _scaffold(taps));
    await tester.tap(find.text('Two'));
    expect(taps, [1]);
  });

  testWidgets('selectVisibleTab(1) selects the second tab of the current route', (
    tester,
  ) async {
    final taps = <int>[];
    await _pump(tester, TargetPlatform.macOS, 1200, _scaffold(taps));
    expect(selectVisibleTab(1), isTrue);
    expect(taps, [1]);
  });

  testWidgets('selectVisibleTab returns false when a route covers the scaffold', (
    tester,
  ) async {
    final taps = <int>[];
    await _pump(tester, TargetPlatform.macOS, 1200, _scaffold(taps));
    final context = tester.element(find.byType(AdaptiveNavScaffold));
    Navigator.of(context).push(
      MaterialPageRoute<void>(builder: (_) => const Scaffold(body: Text('top'))),
    );
    await tester.pumpAndSettle();
    expect(selectVisibleTab(0), isFalse);
    expect(taps, isEmpty);
  });

  testWidgets('selectVisibleTab returns false for an index out of range', (
    tester,
  ) async {
    final taps = <int>[];
    await _pump(tester, TargetPlatform.macOS, 1200, _scaffold(taps));
    expect(selectVisibleTab(2), isFalse);
    expect(selectVisibleTab(-1), isFalse);
    expect(taps, isEmpty);
  });
}
