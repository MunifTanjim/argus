import 'dart:async';

import 'package:argus/pairing/pairing_uri.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/server_api.dart';
import 'package:argus/ui/upgrade_tile.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeServerApi implements ServerApi {
  _FakeServerApi(this.check);
  UpgradeCheck? check;
  final upgraded = <String?>[];
  final pending = <Completer<UpgradeCheck?>>[];
  var holdNext = false;
  var checks = 0;
  Object? restartError;

  @override
  Future<UpgradeCheck?> checkUpgrade(String? nodeId) {
    checks++;
    if (!holdNext) return Future.value(check);
    holdNext = false;
    final c = Completer<UpgradeCheck?>();
    pending.add(c);
    return c.future;
  }

  @override
  Future<(String, String)> upgrade(String? nodeId) async {
    upgraded.add(nodeId);
    final err = restartError;
    if (err != null) throw RestartFailed('1.1.0', err);
    return ('1.0.0', '1.1.0');
  }
}

Future<void> _pump(WidgetTester tester, _FakeServerApi api,
    {String version = '1.0.0', String? nodeId = 'n1', String label = 'macbook', String? url}) async {
  await tester.pumpWidget(ProviderScope(
    overrides: [
      serverApiProvider.overrideWithValue(api),
      if (url != null) credentialsProvider.overrideWith((ref) => GatewayCredentials(url, 't')),
    ],
    child: MaterialApp(home: Scaffold(body: UpgradeTile(nodeId: nodeId, label: label, version: version))),
  ));
  await tester.pump();
}

void main() {
  testWidgets('hidden when the target is current', (tester) async {
    await _pump(tester, _FakeServerApi(const UpgradeCheck(current: '1.1.0', latest: '1.1.0')));
    expect(find.text('Upgrade'), findsNothing);
  });

  testWidgets('hidden when the target does not support upgrade', (tester) async {
    await _pump(tester, _FakeServerApi(null));
    expect(find.text('Upgrade'), findsNothing);
  });

  testWidgets('confirms, then upgrades', (tester) async {
    final api = _FakeServerApi(const UpgradeCheck(current: '1.0.0', latest: '1.1.0'));
    await _pump(tester, api);
    expect(find.text('Version 1.1.0 available'), findsOneWidget);
    await tester.tap(find.widgetWithText(TextButton, 'Upgrade'));
    await tester.pumpAndSettle();
    expect(find.text('Upgrade macbook?'), findsOneWidget);
    await tester.tap(find.widgetWithText(TextButton, 'Upgrade').last);
    await tester.pumpAndSettle();
    expect(api.upgraded, ['n1']);
    expect(find.text('Upgraded to 1.1.0. Restarting…'), findsOneWidget);
  });

  testWidgets('warns that a quick tunnel gateway gets a new URL', (tester) async {
    final api = _FakeServerApi(const UpgradeCheck(current: '1.0.0', latest: '1.1.0'));
    await _pump(tester, api, nodeId: null, label: 'the gateway', url: 'wss://a-b-c.trycloudflare.com/ws');
    await tester.tap(find.widgetWithText(TextButton, 'Upgrade'));
    await tester.pumpAndSettle();
    expect(find.textContaining('must pair again'), findsOneWidget);
  });

  testWidgets('no quick tunnel warning for a node or a named tunnel', (tester) async {
    final api = _FakeServerApi(const UpgradeCheck(current: '1.0.0', latest: '1.1.0'));
    await _pump(tester, api, url: 'wss://a-b-c.trycloudflare.com/ws');
    await tester.tap(find.widgetWithText(TextButton, 'Upgrade'));
    await tester.pumpAndSettle();
    expect(find.textContaining('must pair again'), findsNothing);
    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await tester.pumpAndSettle();

    await _pump(tester, api, nodeId: null, label: 'the gateway', url: 'wss://argus.example.com/ws');
    await tester.tap(find.widgetWithText(TextButton, 'Upgrade'));
    await tester.pumpAndSettle();
    expect(find.textContaining('must pair again'), findsNothing);
  });

  testWidgets('cancel does not upgrade', (tester) async {
    final api = _FakeServerApi(const UpgradeCheck(current: '1.0.0', latest: '1.1.0'));
    await _pump(tester, api);
    await tester.tap(find.widgetWithText(TextButton, 'Upgrade'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await tester.pumpAndSettle();
    expect(api.upgraded, isEmpty);
  });

  testWidgets('clears after the target restarts on the new version', (tester) async {
    final api = _FakeServerApi(const UpgradeCheck(current: '1.0.0', latest: '1.1.0'));
    await _pump(tester, api);
    await tester.tap(find.widgetWithText(TextButton, 'Upgrade'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(TextButton, 'Upgrade').last);
    await tester.pumpAndSettle();
    expect(find.text('Upgraded to 1.1.0. Restarting…'), findsOneWidget);
    api.check = const UpgradeCheck(current: '1.1.0', latest: '1.1.0');
    await _pump(tester, api, version: '1.1.0');
    expect(find.text('Upgrade'), findsNothing);
    expect(find.text('Upgraded to 1.1.0. Restarting…'), findsNothing);
  });

  testWidgets('a stale check resolving late does not bring the tile back', (tester) async {
    final api = _FakeServerApi(const UpgradeCheck(current: '1.1.0', latest: '1.1.0'))..holdNext = true;
    await _pump(tester, api);
    await _pump(tester, api, version: '1.1.0');
    api.pending.single.complete(const UpgradeCheck(current: '1.0.0', latest: '1.1.0'));
    await tester.pump();
    expect(find.text('Upgrade'), findsNothing);
  });

  testWidgets('a failed restart says the binary was replaced', (tester) async {
    final api = _FakeServerApi(const UpgradeCheck(current: '1.0.0', latest: '1.1.0'))
      ..restartError = StateError('boom');
    await _pump(tester, api);
    await tester.tap(find.widgetWithText(TextButton, 'Upgrade'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(TextButton, 'Upgrade').last);
    await tester.pumpAndSettle();
    expect(find.text('Upgraded to 1.1.0. Restart failed: boom'), findsOneWidget);
  });

  testWidgets('re-checks when the version stays the same after a restart', (tester) async {
    final api = _FakeServerApi(const UpgradeCheck(current: '1.0.0', latest: '1.1.0'));
    await _pump(tester, api);
    await tester.tap(find.widgetWithText(TextButton, 'Upgrade'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(TextButton, 'Upgrade').last);
    await tester.pumpAndSettle();
    expect(find.text('Upgraded to 1.1.0. Restarting…'), findsOneWidget);
    expect(api.checks, 1);
    await tester.pump(const Duration(seconds: 15));
    await tester.pump();
    expect(api.checks, 2);
    expect(find.text('Upgraded to 1.1.0. Restarting…'), findsNothing);
  });

  testWidgets('a version change while confirming aborts the upgrade', (tester) async {
    final api = _FakeServerApi(const UpgradeCheck(current: '1.0.0', latest: '1.1.0'));
    await _pump(tester, api);
    await tester.tap(find.widgetWithText(TextButton, 'Upgrade'));
    await tester.pumpAndSettle();
    await _pump(tester, api, version: '1.0.1');
    await tester.tap(find.widgetWithText(TextButton, 'Upgrade').last);
    await tester.pumpAndSettle();
    expect(api.upgraded, isEmpty);
  });
}
