import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:macos_window_utils/macos_window_utils.dart';
import 'package:argus/main.dart';
import 'package:argus/ui/titlebar.dart';

const _windowChannel = MethodChannel('macos_window_utils/window_manipulator');

void main() {
  setUpAll(() async {
    TestWidgetsFlutterBinding.ensureInitialized();
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(
          _windowChannel,
          (call) async => call.method == 'isWindowFullscreened' ? false : true,
        );
    await WindowManipulator.initialize();
  });

  testWidgets('macOS: no 28 px strip exists any more; the loading screen '
      'starts with the toolbar row at the top of the window', (tester) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);
    FlutterSecureStorage.setMockInitialValues({});
    final messenger = tester.binding.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(SystemChannels.menu, (_) async => null);
    addTearDown(
      () => messenger.setMockMethodCallHandler(SystemChannels.menu, null),
    );

    await tester.pumpWidget(const ProviderScope(child: ArgusApp()));

    expect(find.byKey(const ValueKey('window-strip')), findsNothing);
    expect(
      tester.getRect(find.byType(TitlebarSpacer)),
      const Rect.fromLTWH(0, 0, 800, kMacToolbarHeight),
    );
  });
}
