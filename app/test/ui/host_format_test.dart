import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/host_info.dart';
import 'package:argus/ui/host_format.dart';

void main() {
  test('formatUptime', () {
    expect(formatUptime(0), '<1m');
    expect(formatUptime(59), '<1m');
    expect(formatUptime(720), '12m');
    expect(formatUptime(15120), '4h 12m');
    expect(formatUptime(274320), '3d 4h 12m');
    expect(formatUptime(259500), '3d 0h 5m');
  });

  test('formatBattery', () {
    expect(formatBattery(const HostBattery(percent: 82, state: 'charging')), '82% · charging');
    expect(formatBattery(const HostBattery(percent: 40, state: 'not_charging')), '40% · not charging');
    expect(formatBattery(const HostBattery(percent: 7, state: 'unknown')), '7%');
    expect(formatBattery(const HostBattery(percent: 55, state: 'discharging')), '55% · discharging');
    expect(formatBattery(const HostBattery(percent: 100, state: 'full')), '100% · full');
  });

  test('untilClock shows the date only on another day', () {
    final now = DateTime(2026, 10, 3, 9);
    expect(untilClock(DateTime(2026, 10, 3, 14, 30), now), '14:30');
    expect(untilClock(DateTime(2026, 10, 4, 14, 30), now), 'Oct 4 14:30');
  });
}
