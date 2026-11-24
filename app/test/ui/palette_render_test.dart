import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/appearance.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/ui/appearance_screen.dart';
import 'package:argus/ui/blocked_card.dart';
import 'package:argus/ui/session_sections_list.dart';
import 'package:argus/ui/status_tiles.dart';
import 'package:argus/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/home_fixtures.dart';

class _FakeKv implements SecureKv {
  final Map<String, String> m = {};
  @override
  Future<String?> read(String key) async => m[key];
  @override
  Future<void> write(String key, String value) async => m[key] = value;
  @override
  Future<void> delete(String key) async => m.remove(key);
}

/// Pumps [child] on a phone-sized screen under [p], and fails on any
/// exception, overflow included.
Future<void> _render(
  WidgetTester tester,
  ArgusPalette p,
  Widget child, {
  StatusStyle statusStyle = StatusStyle.stripe,
}) async {
  tester.view.physicalSize = const Size(390 * 3, 844 * 3);
  tester.view.devicePixelRatio = 3;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        appearanceStoreProvider.overrideWithValue(AppearanceStore(_FakeKv())),
        sessionsLoadingProvider.overrideWith((ref) => false),
      ],
      child: MaterialApp(
        theme: buildArgusTheme(
          palette: p,
          prefs: AppearancePrefs(statusStyle: statusStyle),
        ),
        home: child,
      ),
    ),
  );
  await tester.pumpAndSettle();
  expect(tester.takeException(), isNull);
}

void main() {
  final palettes = [
    for (final id in ArgusPaletteId.values)
      for (final b in Brightness.values) paletteFor(id, b),
  ];
  final blocked = homeFixtureSessions.firstWhere((s) => s.blocked);

  for (final p in palettes) {
    group('${p.name} ${p.brightness.name}', () {
      for (final style in StatusStyle.values) {
        testWidgets('home, ${style.name} status', (tester) async {
          await _render(
            tester,
            p,
            Scaffold(
              body: Column(
                children: [
                  StatusTiles(sessions: homeFixtureSessions),
                  Expanded(
                    child: SessionSectionsList(
                      sessions: homeFixtureSessions,
                      groupBy: GroupBy.status,
                      applyStatusFilter: true,
                    ),
                  ),
                ],
              ),
            ),
            statusStyle: style,
          );
          expect(find.byType(BlockedCard), findsOneWidget);
        });
      }

      testWidgets('blocked card', (tester) async {
        await _render(
          tester,
          p,
          Scaffold(
            body: BlockedCard(session: blocked, showNode: true),
          ),
        );
        expect(find.text('flutter test'), findsOneWidget);
      });

      testWidgets('appearance screen', (tester) async {
        await _render(tester, p, const AppearanceScreen());
        expect(find.text('Palette'), findsOneWidget);
      });
    });
  }
}
