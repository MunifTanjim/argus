import 'dart:async';

import 'package:argus/main.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/push/push_controller.dart';
import 'package:argus/state/appearance.dart';
import 'package:argus/state/push.dart';
import 'package:argus/ui/profiles_screen.dart';
import 'package:argus/ui/theme.dart';
import 'package:argus/ui/theme/scaled_text_scaler.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeKv implements SecureKv {
  final Map<String, String> m = {};
  @override
  Future<String?> read(String key) async => m[key];
  @override
  Future<void> write(String key, String value) async => m[key] = value;
  @override
  Future<void> delete(String key) async => m.remove(key);
}

/// Grows small text more than large text, like Android's nonlinear scaling.
class _NonlinearScaler extends TextScaler {
  const _NonlinearScaler();
  @override
  double scale(double fontSize) => fontSize < 20 ? fontSize * 1.3 : fontSize;
  @override
  double get textScaleFactor => 1.3;
}

class _GatedKv extends _FakeKv {
  _GatedKv(this.gate);
  final Completer<void> gate;
  @override
  Future<String?> read(String key) async {
    await gate.future;
    return m[key];
  }
}

Future<ProviderContainer> _pumpApp(
  WidgetTester tester,
  SecureKv kv, {
  TextScaler? osScaler,
}) async {
  // migrateLegacyOnce and the profile stores use the real secure storage.
  FlutterSecureStorage.setMockInitialValues({});
  final container = ProviderContainer(
    overrides: [
      appearanceStoreProvider.overrideWithValue(AppearanceStore(kv)),
      // Skip push init: it talks to platform plugins.
      pushControllerProvider.overrideWith(
        (ref) => PushController(providerKv: _FakeKv()),
      ),
    ],
  );
  addTearDown(container.dispose);
  Widget app = const ArgusApp();
  if (osScaler != null) {
    // WidgetsApp inherits an existing MediaQuery, so this stands in for the
    // platform's scaler.
    app = MediaQuery(
      data: MediaQueryData.fromView(tester.view).copyWith(textScaler: osScaler),
      child: app,
    );
  }
  await tester.pumpWidget(
    UncontrolledProviderScope(container: container, child: app),
  );
  return container;
}

Color _scaffoldBg(WidgetTester tester) => Theme.of(
  tester.element(find.byType(Scaffold).last),
).scaffoldBackgroundColor;

void main() {
  testWidgets('theme change keeps navigator stack and open sheet', (
    tester,
  ) async {
    final c = await _pumpApp(tester, _FakeKv());
    await tester.pumpAndSettle();

    final nav = tester.state<NavigatorState>(find.byType(Navigator).first);
    nav.push(
      MaterialPageRoute<void>(
        builder: (_) => const Scaffold(body: Text('second route')),
      ),
    );
    await tester.pumpAndSettle();
    showModalBottomSheet<void>(
      context: tester.element(find.text('second route')),
      builder: (_) => const Text('open sheet'),
    );
    await tester.pumpAndSettle();
    expect(find.text('open sheet'), findsOneWidget);

    await c
        .read(appearancePrefsProvider.notifier)
        .setPalette(ArgusPaletteId.catppuccin);
    await tester.pumpAndSettle();

    expect(find.text('open sheet'), findsOneWidget);
    expect(find.text('second route'), findsOneWidget);
    expect(nav.canPop(), isTrue);
    expect(
      Theme.of(
        tester.element(find.text('second route')),
      ).scaffoldBackgroundColor,
      catppuccinMocha.bg0,
    );
    expect(
      tester
          .widget<Material>(
            find
                .ancestor(
                  of: find.text('second route'),
                  matching: find.byType(Material),
                )
                .first,
          )
          .color,
      catppuccinMocha.bg0,
    );
  });

  testWidgets('app waits for appearance prefs before the first screen', (
    tester,
  ) async {
    final gate = Completer<void>();
    final kv = _GatedKv(gate)..m['appearance.palette'] = 'nord';
    await _pumpApp(tester, kv);
    await tester.pump();
    await tester.pump();
    expect(find.byType(CircularProgressIndicator), findsOneWidget);

    gate.complete();
    // No settle: the first frame of the first screen must already use the
    // saved palette, without a fade from the default. One frame applies the
    // loaded theme; the next shows the first screen.
    await tester.pump();
    await tester.pump();

    expect(find.byType(ProfilesScreen), findsOneWidget);
    expect(_scaffoldBg(tester), nord.bg0);
  });

  testWidgets('text size change keeps the navigator focus scope', (
    tester,
  ) async {
    final c = await _pumpApp(tester, _FakeKv());
    await tester.pumpAndSettle();
    final scope = find.byWidgetPredicate(
      (w) => w is FocusScope && w.debugLabel == 'Navigator Scope',
    );
    final before = tester.state(scope);

    await c.read(appearancePrefsProvider.notifier).setTextScale(1.15);
    await tester.pumpAndSettle();
    expect(tester.state(scope), same(before));

    await c.read(appearancePrefsProvider.notifier).setTextScale(1.0);
    await tester.pumpAndSettle();
    expect(tester.state(scope), same(before));
  });

  group('text size multiplies the OS text scale', () {
    // 1.3: the profiles header has a fixed 96px height and overflows near
    // 1.55x total scale.
    void osTextScale(WidgetTester tester, double v) {
      tester.platformDispatcher.textScaleFactorTestValue = v;
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    }

    double scaleInApp(WidgetTester tester) => MediaQuery.textScalerOf(
      tester.element(find.byType(ProfilesScreen)),
    ).scale(14);

    testWidgets('at 115%', (tester) async {
      osTextScale(tester, 1.3);
      final kv = _FakeKv()..m['appearance.textScale'] = '1.15';
      await _pumpApp(tester, kv);
      await tester.pumpAndSettle();
      expect(scaleInApp(tester), closeTo(14 * 1.3 * 1.15, 1e-9));
    });

    testWidgets('at 100% the OS scaler is used as is', (tester) async {
      osTextScale(tester, 1.3);
      await _pumpApp(tester, _FakeKv());
      await tester.pumpAndSettle();
      final ctx = tester.element(find.byType(ProfilesScreen));
      expect(scaleInApp(tester), closeTo(14 * 1.3, 1e-9));
      expect(MediaQuery.textScalerOf(ctx), isNot(isA<ScaledTextScaler>()));
    });

    testWidgets('keeps nonlinear OS scaling', (tester) async {
      final kv = _FakeKv()..m['appearance.textScale'] = '1.15';
      await _pumpApp(tester, kv, osScaler: const _NonlinearScaler());
      await tester.pumpAndSettle();
      final scaler = MediaQuery.textScalerOf(
        tester.element(find.byType(ProfilesScreen)),
      );
      expect(scaler.scale(14), closeTo(14 * 1.3 * 1.15, 1e-9));
      expect(scaler.scale(28), closeTo(28 * 1.15, 1e-9));
    });

    testWidgets('at 100% a nonlinear OS scaler is kept as is', (tester) async {
      await _pumpApp(tester, _FakeKv(), osScaler: const _NonlinearScaler());
      await tester.pumpAndSettle();
      expect(
        MediaQuery.textScalerOf(tester.element(find.byType(ProfilesScreen))),
        isA<_NonlinearScaler>(),
      );
    });
  });
}
