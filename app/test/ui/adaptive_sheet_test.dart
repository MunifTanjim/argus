import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:argus/ui/adaptive_sheet.dart';
import 'package:argus/ui/theme.dart';
import 'package:argus/ui/titlebar.dart';
import 'package:macos_window_utils/macos_window_utils.dart';

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
      home: Align(
        alignment: Alignment.topLeft,
        child: Builder(
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
    ),
  );
  await tester.tap(find.text('open'));
  await tester.pumpAndSettle();
  return r;
}

const _windowChannel = MethodChannel('macos_window_utils/window_manipulator');

void main() {
  setUpAll(() async {
    TestWidgetsFlutterBinding.ensureInitialized();
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(_windowChannel, (call) async => true);
    await WindowManipulator.initialize();
  });

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

  testWidgets('the wide popup opens next to a small button', (tester) async {
    await _open(tester, TargetPlatform.macOS);
    final button = tester.getRect(find.text('open'));
    final first = tester.getRect(find.text('Title'));
    expect(first.top, lessThan(button.bottom + 40));
    expect(first.left, lessThan(button.right + 40));
  });

  testWidgets('a sheet header replaces the title in the sheet only', (
    tester,
  ) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.android;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) => TextButton(
            onPressed: () => showActionMenu<String>(
              context,
              title: 'T',
              sheetHeader: const Text('HEADER'),
              items: _items,
            ),
            child: const Text('open'),
          ),
        ),
      ),
    );
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    expect(find.text('HEADER'), findsOneWidget);
    expect(find.text('T'), findsNothing);
  });

  group('showSidePanel', () {
    Future<void> openPanel(WidgetTester tester, TargetPlatform platform) async {
      debugDefaultTargetPlatformOverride = platform;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
      tester.view.physicalSize = const Size(1200, 800);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      await tester.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (context) => TextButton(
              onPressed: () => showSidePanel<void>(
                context,
                builder: (_) => const Text('PANEL'),
              ),
              child: const Text('open'),
            ),
          ),
        ),
      );
      await tester.tap(find.text('open'));
      await tester.pumpAndSettle();
    }

    testWidgets('showSidePanel opens a bottom sheet on iOS at 1200 px', (
      tester,
    ) async {
      await openPanel(tester, TargetPlatform.iOS);
      expect(find.byType(BottomSheet), findsOneWidget);
      expect(find.text('PANEL'), findsOneWidget);
    });

    testWidgets(
      'showSidePanel opens a 400 px panel at the right edge on macOS at 1200 px',
      (tester) async {
        await openPanel(tester, TargetPlatform.macOS);
        expect(find.byType(BottomSheet), findsNothing);
        final panel = tester.getRect(
          find
              .ancestor(of: find.text('PANEL'), matching: find.byType(SizedBox))
              .first,
        );
        expect(panel.right, 1200);
        expect(panel.width, 400);
        expect(panel.height, 800);
      },
    );

    testWidgets('macOS: the panel content starts below the toolbar row', (
      tester,
    ) async {
      await openPanel(tester, TargetPlatform.macOS);
      expect(
        tester.getTopLeft(find.text('PANEL')).dy,
        greaterThanOrEqualTo(kMacToolbarHeight),
      );
    });

    testWidgets('Linux: the panel content starts at the top', (tester) async {
      await openPanel(tester, TargetPlatform.linux);
      expect(tester.getTopLeft(find.text('PANEL')).dy, lessThan(kMacToolbarHeight));
    });

    testWidgets('a click on the scrim closes the panel', (tester) async {
      await openPanel(tester, TargetPlatform.macOS);
      await tester.tapAt(const Offset(100, 400));
      await tester.pumpAndSettle();
      expect(find.text('PANEL'), findsNothing);
    });

    for (final (platform, width) in [
      (TargetPlatform.android, 1200.0),
      (TargetPlatform.macOS, 800.0),
    ]) {
      testWidgets(
        'the narrow path is a root-navigator bottom sheet on $platform at '
        '${width.toInt()} px',
        (tester) async {
          debugDefaultTargetPlatformOverride = platform;
          addTearDown(() => debugDefaultTargetPlatformOverride = null);
          tester.view.physicalSize = Size(width, 800);
          tester.view.devicePixelRatio = 1;
          addTearDown(tester.view.reset);
          final nested = GlobalKey<NavigatorState>();
          await tester.pumpWidget(
            MaterialApp(
              home: Navigator(
                key: nested,
                onGenerateRoute: (_) => MaterialPageRoute<void>(
                  builder: (_) => Builder(
                    builder: (context) => TextButton(
                      onPressed: () => showSidePanel<void>(
                        context,
                        builder: (_) => const Text('PANEL'),
                      ),
                      child: const Text('open'),
                    ),
                  ),
                ),
              ),
            ),
          );
          await tester.tap(find.text('open'));
          await tester.pumpAndSettle();
          expect(find.byType(BottomSheet), findsOneWidget);
          final sheetRoute = ModalRoute.of(tester.element(find.text('PANEL')))!;
          expect(sheetRoute.navigator, isNot(nested.currentState));
          expect(
            sheetRoute.navigator,
            Navigator.of(nested.currentContext!, rootNavigator: true),
          );
        },
      );
    }
  });
}
