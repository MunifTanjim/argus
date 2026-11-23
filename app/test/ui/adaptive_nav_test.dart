import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:argus/ui/adaptive_nav.dart';

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

void main() {
  testWidgets('shows a bottom NavigationBar on iOS at 1200 px', (tester) async {
    await _pump(tester, TargetPlatform.iOS, 1200, _scaffold([]));
    expect(find.byType(NavigationBar), findsOneWidget);
    expect(find.byType(NavigationRail), findsNothing);
  });

  testWidgets('shows a NavigationRail on macOS at 1200 px', (tester) async {
    await _pump(tester, TargetPlatform.macOS, 1200, _scaffold([]));
    expect(find.byType(NavigationRail), findsOneWidget);
    expect(find.byType(NavigationBar), findsNothing);
    expect(find.text('body'), findsOneWidget);
  });

  testWidgets('shows a bottom NavigationBar on macOS at 800 px', (tester) async {
    await _pump(tester, TargetPlatform.macOS, 800, _scaffold([]));
    expect(find.byType(NavigationBar), findsOneWidget);
    expect(find.byType(NavigationRail), findsNothing);
  });

  testWidgets('a rail tap calls onSelected with the index', (tester) async {
    final taps = <int>[];
    await _pump(tester, TargetPlatform.macOS, 1200, _scaffold(taps));
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
