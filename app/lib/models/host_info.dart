/// The HostWakelock.until value for "until turned off". Mirrors Go's
/// api.HostWakelockIndefinite.
const wakelockIndefinite = '9999-12-31T23:59:59Z';

class HostInfo {
  const HostInfo({this.os = '', required this.uptimeSeconds, this.battery, required this.wakelock});

  factory HostInfo.fromJson(Map<String, dynamic> j) => HostInfo(
    os: j['os'] as String? ?? '',
    uptimeSeconds: (j['uptime_seconds'] as num?)?.toInt() ?? 0,
    battery: j['battery'] is Map
        ? HostBattery.fromJson((j['battery'] as Map).cast<String, dynamic>())
        : null,
    wakelock: HostWakelock.fromJson(
      (j['wakelock'] as Map?)?.cast<String, dynamic>() ?? const {},
    ),
  );

  final String os;
  final int uptimeSeconds;
  final HostBattery? battery;
  final HostWakelock wakelock;
}

class HostBattery {
  const HostBattery({required this.percent, required this.state});

  factory HostBattery.fromJson(Map<String, dynamic> j) => HostBattery(
    percent: (j['percent'] as num?)?.toInt() ?? 0,
    state: j['state'] as String? ?? 'unknown',
  );

  final int percent;
  final String state;
}

class HostWakelock {
  const HostWakelock({this.until = ''});

  factory HostWakelock.fromJson(Map<String, dynamic> j) =>
      HostWakelock(until: j['until'] as String? ?? '');

  final String until;

  bool get on => until.isNotEmpty;
}
