import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:argus/ui/adaptive_sheet.dart';
import 'package:argus/ui/theme.dart';

const _items = [
  ActionItem<String>(value: 'a', label: 'Alpha', icon: Icon(Icons.add)),
  ActionItem<String>(
    value: 'd',
    label: 'Danger',
    icon: Icon(Icons.delete),
    danger: true,
  ),
  ActionItem<String>(value: 'x', label: 'Disabled', enabled: false),
];

class _Result {
  String? value;
  bool done = false;
}

Future<_Result> _open(
  WidgetTester tester,
  TargetPlatform platform, {
  String? title = 'Title',
}) async {
  debugDefaultTargetPlatformOverride = platform;
  addTearDown(() => debugDefaultTargetPlatformOverride = null);
  tester.view.physicalSize = const Size(1200, 800);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final r = _Result();
  await tester.pumpWidget(
    MaterialApp(
      home: Builder(
        builder: (context) => TextButton(
          onPressed: () async {
            r.value = await showActionMenu<String>(
              context,
              title: title,
              items: _items,
            );
            r.done = true;
          },
          child: const Text('open'),
        ),
      ),
    ),
  );
  await tester.tap(find.text('open'));
  await tester.pumpAndSettle();
  return r;
}

void main() {
  testWidgets('iOS at 1200 px opens a bottom sheet', (tester) async {
    await _open(tester, TargetPlatform.iOS);
    expect(find.byType(BottomSheet), findsOneWidget);
    expect(find.text('Alpha'), findsOneWidget);
  });

  testWidgets('macOS at 1200 px opens a popup menu', (tester) async {
    final r = await _open(tester, TargetPlatform.macOS);
    expect(find.byType(BottomSheet), findsNothing);
    expect(find.text('Alpha'), findsOneWidget);
    expect(find.text('Danger'), findsOneWidget);
    await tester.tap(find.text('Alpha'));
    await tester.pumpAndSettle();
    expect(r.value, 'a');
  });

  for (final platform in [TargetPlatform.iOS, TargetPlatform.macOS]) {
    testWidgets('a danger item uses AppColors.error on $platform', (
      tester,
    ) async {
      await _open(tester, platform);
      expect(
        tester.widget<Text>(find.text('Danger')).style?.color,
        AppColors.error,
      );
      expect(
        IconTheme.of(tester.element(find.byIcon(Icons.delete))).color,
        AppColors.error,
      );
      expect(
        IconTheme.of(tester.element(find.byIcon(Icons.add))).color,
        isNot(AppColors.error),
      );
    });

    testWidgets('a disabled item does not return a value on $platform', (
      tester,
    ) async {
      final r = await _open(tester, platform);
      await tester.tap(find.text('Disabled'));
      await tester.pumpAndSettle();
      expect(r.done, isFalse);
    });

    testWidgets('a dismissed menu returns null on $platform', (tester) async {
      final r = await _open(tester, platform);
      await tester.tapAt(const Offset(5, 5));
      await tester.pumpAndSettle();
      expect(r.done, isTrue);
      expect(r.value, isNull);
    });
  }

  testWidgets('the title shows as a non-tappable header in the popup', (
    tester,
  ) async {
    final r = await _open(tester, TargetPlatform.macOS);
    final title = find.text('Title');
    expect(title, findsOneWidget);
    expect(tester.widget<Text>(title).style?.fontWeight, FontWeight.w600);
    final item = tester.widget<PopupMenuItem<String>>(
      find.ancestor(of: title, matching: find.byType(PopupMenuItem<String>)),
    );
    expect(item.enabled, isFalse);
    await tester.tap(title);
    await tester.pumpAndSettle();
    expect(r.done, isFalse);
  });
}
