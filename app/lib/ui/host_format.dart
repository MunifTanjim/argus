import '../models/host_info.dart';

String formatUptime(int secs) {
  final units = [(secs ~/ 86400, 'd'), (secs % 86400 ~/ 3600, 'h'), (secs % 3600 ~/ 60, 'm')];
  final parts = <String>[];
  for (final (n, unit) in units) {
    if (n > 0 || parts.isNotEmpty) parts.add('$n$unit');
  }
  return parts.isEmpty ? '<1m' : parts.join(' ');
}

const _batteryStates = {
  'charging': 'charging',
  'discharging': 'discharging',
  'full': 'full',
  'not_charging': 'not charging',
};

String formatBattery(HostBattery b) {
  final st = _batteryStates[b.state];
  return st == null ? '${b.percent}%' : '${b.percent}% · $st';
}

const _months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/// Expects local times.
String untilClock(DateTime until, DateTime now) {
  final hh = until.hour.toString().padLeft(2, '0');
  final mm = until.minute.toString().padLeft(2, '0');
  final sameDay = until.year == now.year && until.month == now.month && until.day == now.day;
  return sameDay ? '$hh:$mm' : '${_months[until.month - 1]} ${until.day} $hh:$mm';
}
