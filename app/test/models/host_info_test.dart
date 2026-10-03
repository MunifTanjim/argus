import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/host_info.dart';

void main() {
  test('HostInfo.fromJson reads battery and wakelock', () {
    final h = HostInfo.fromJson({
      'os': 'macOS 26.0.1 (arm64)',
      'uptime_seconds': 90,
      'battery': {'percent': 82, 'state': 'charging'},
      'wakelock': {'until': wakelockIndefinite},
    });
    expect(h.os, 'macOS 26.0.1 (arm64)');
    expect(h.uptimeSeconds, 90);
    expect(h.battery!.percent, 82);
    expect(h.battery!.state, 'charging');
    expect(h.wakelock.on, isTrue);
  });

  test('HostInfo.fromJson without battery or wakelock', () {
    final h = HostInfo.fromJson({'uptime_seconds': 5});
    expect(h.os, '');
    expect(h.battery, isNull);
    expect(h.wakelock.on, isFalse);
  });
}
