import 'package:flutter_test/flutter_test.dart';
import 'package:argus/state/host_api.dart';

import '../support/fake_gateway_client.dart';

void main() {
  test('info and setWakelock send node_id', () async {
    final c = FakeGatewayClient((m, p) async => switch (m) {
      'host.info' => {'uptime_seconds': 1, 'wakelock': <String, dynamic>{}},
      'host.setWakelock' => {'until': '2026-10-03T12:30:00Z'},
      _ => null,
    });
    final api = HostApi(() => c);
    final info = await api.info('A');
    expect(info.uptimeSeconds, 1);
    expect(c.calls.last.$1, 'host.info');
    expect(c.calls.last.$2, {'node_id': 'A'});
    final w = await api.setWakelock('A', '2026-10-03T12:30:00Z');
    expect(w.until, '2026-10-03T12:30:00Z');
    expect(c.calls.last.$1, 'host.setWakelock');
    expect(c.calls.last.$2, {'node_id': 'A', 'until': '2026-10-03T12:30:00Z'});
  });
}
