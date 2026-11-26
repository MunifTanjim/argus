import 'relative_time.dart';

const _months = [
  'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', //
  'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
];

const _weekdays = [
  'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', //
  'Sunday',
];

/// A list-row age: "2h", "3d", "4mo", or "now". '' when [iso] does not parse.
String shortAgo(String? iso, [DateTime? now]) {
  final then = DateTime.tryParse(iso ?? '');
  if (then == null) return '';
  final rel = relativeTimeFrom(then, now);
  return rel == 'just now' ? 'now' : rel.replaceFirst(' ago', '');
}

/// The day header for local [day]: "Today", "Yesterday", the weekday within
/// the last week, then "Nov 3", with the year when it is not [now]'s.
String dayLabel(DateTime day, DateTime now) {
  final d = DateTime(day.year, day.month, day.day);
  final today = DateTime(now.year, now.month, now.day);
  // Calendar days, rounded so a DST shift does not change the count.
  final back = (today.difference(d).inHours / 24).round();
  if (back == 0) return 'Today';
  if (back == 1) return 'Yesterday';
  if (back > 1 && back < 7) return _weekdays[d.weekday - 1];
  final md = '${_months[d.month - 1]} ${d.day}';
  return d.year == now.year ? md : '$md, ${d.year}';
}

/// "09:05", or "09:05:07" with [seconds].
String clockTime(DateTime t, {bool seconds = false}) {
  String p(int n) => n.toString().padLeft(2, '0');
  final hm = '${p(t.hour)}:${p(t.minute)}';
  return seconds ? '$hm:${p(t.second)}' : hm;
}

/// A turn's time, with only the date parts that differ from [now]: the time
/// today, "Nov 25 09:03:05" earlier this year, and the year too before that.
/// The month stays with the day, since a day alone reads like a bare number.
String turnStamp(DateTime t, DateTime now) {
  final time = clockTime(t, seconds: true);
  if (t.year == now.year && t.month == now.month && t.day == now.day) {
    return time;
  }
  final day = '${_months[t.month - 1]} ${t.day}';
  return t.year == now.year ? '$day $time' : '${t.year} $day $time';
}

/// "45s", "34m", "1h", "1h 12m"; '' when [ms] is not positive.
String compactDuration(int ms) {
  if (ms <= 0) return '';
  final s = ms ~/ 1000;
  if (s < 60) return '${s}s';
  if (s < 3600) return '${s ~/ 60}m';
  final m = s ~/ 60 % 60;
  return m == 0 ? '${s ~/ 3600}h' : '${s ~/ 3600}h ${m}m';
}

/// "950", "48.2k", "1.3M".
String compactCount(int n) {
  if (n >= 1000000) return '${(n / 1000000).toStringAsFixed(1)}M';
  if (n >= 1000) return '${(n / 1000).toStringAsFixed(1)}k';
  return '$n';
}

final _home = RegExp(r'^/(Users|home)/[^/]+');

/// [cwd] with the home folder as "~" and the middle cut when more than three
/// segments remain: "~/Dev/…/argus".
String shortenPath(String cwd) {
  final tilde = cwd.replaceFirst(_home, '~');
  final lead = tilde.startsWith('~') ? '~' : '';
  final segs = tilde.substring(lead.length).split('/')
    ..removeWhere((s) => s.isEmpty);
  if (segs.length <= 3) return tilde;
  return '$lead/${segs.first}/…/${segs.last}';
}
