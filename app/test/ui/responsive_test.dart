import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:argus/ui/responsive.dart';
import 'package:argus/util/platform.dart';

Future<bool> _desktopLayoutAt(
  WidgetTester tester,
  TargetPlatform platform,
  double width,
) async {
  debugDefaultTargetPlatformOverride = platform;
  tester.view.physicalSize = Size(width, 700);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  late bool result;
  await tester.pumpWidget(
    MaterialApp(
      home: Builder(
        builder: (context) {
          result = useDesktopLayout(context);
          return const SizedBox();
        },
      ),
    ),
  );
  debugDefaultTargetPlatformOverride = null;
  return result;
}

void main() {
  testWidgets('useDesktopLayout is true on macOS at 900 px', (tester) async {
    expect(await _desktopLayoutAt(tester, TargetPlatform.macOS, 900), isTrue);
  });

  testWidgets('useDesktopLayout is false on macOS at 899 px', (tester) async {
    expect(await _desktopLayoutAt(tester, TargetPlatform.macOS, 899), isFalse);
  });

  testWidgets('useDesktopLayout is false on iOS at 1200 px', (tester) async {
    expect(await _desktopLayoutAt(tester, TargetPlatform.iOS, 1200), isFalse);
  });

  testWidgets('useDesktopLayout is false on Android at 1200 px', (
    tester,
  ) async {
    expect(
      await _desktopLayoutAt(tester, TargetPlatform.android, 1200),
      isFalse,
    );
  });

  test('isDesktop is true on linux and false on android', () {
    debugDefaultTargetPlatformOverride = TargetPlatform.linux;
    expect(isDesktop, isTrue);
    debugDefaultTargetPlatformOverride = TargetPlatform.android;
    expect(isDesktop, isFalse);
    debugDefaultTargetPlatformOverride = null;
  });
}
