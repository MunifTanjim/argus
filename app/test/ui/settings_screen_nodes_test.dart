import 'package:argus/models/host_info.dart';
import 'package:argus/state/control.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/state/host_api.dart';
import 'package:argus/transport/connection.dart';
import 'package:argus/ui/node_screen.dart';
import 'package:argus/ui/settings_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeApi implements HostApi {
  @override
  Future<HostInfo> info(String nodeId) async => const HostInfo(
    os: 'macOS',
    uptimeSeconds: 1,
    wakelock: HostWakelock(),
  );

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

void main() {
  testWidgets('tapping a node line opens the node screen', (tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          credentialsProvider.overrideWith((ref) => null),
          gatewayProvider.overrideWithValue(null),
          connStateProvider.overrideWith((ref) => ConnState.connected),
          hostApiProvider.overrideWithValue(_FakeApi()),
          serverInfoProvider.overrideWith(
            (ref) async =>
                ServerInfo(version: '', nodes: [NodeRef('A', 'macbook')]),
          ),
        ],
        child: const MaterialApp(home: SettingsScreen()),
      ),
    );
    await tester.pump();

    await tester.ensureVisible(find.byKey(const Key('settings-node-A')));
    await tester.tap(find.byKey(const Key('settings-node-A')));
    await tester.pumpAndSettle();
    final screen = tester.widget<NodeScreen>(find.byType(NodeScreen));
    expect(screen.nodeId, 'A');
    expect(screen.label, 'macbook');
  });
}
