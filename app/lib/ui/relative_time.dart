/// Returns a compact relative time string like "3h ago" or "just now".
///
/// Mirrors the `relativeTime` function from `web/src/components/History.tsx`.
/// Returns '' for null, empty, or unparseable [iso] strings.
/// [now] defaults to [DateTime.now()] when omitted.
String relativeTime(String? iso, [DateTime? now]) {
  if (iso == null || iso.isEmpty) return '';
  final then = DateTime.tryParse(iso);
  if (then == null) return '';
  return relativeTimeFrom(then, now);
}

/// Like [relativeTime] but from an already-parsed [then].
String relativeTimeFrom(DateTime then, [DateTime? now]) {
  final secs = ((now ?? DateTime.now()).difference(then).inMilliseconds / 1000)
      .clamp(0, double.infinity);
  const units = [
    (86400 * 365, 'y'),
    (86400 * 30, 'mo'),
    (86400 * 7, 'w'),
    (86400, 'd'),
    (3600, 'h'),
    (60, 'm'),
  ];
  for (final (size, label) in units) {
    if (secs >= size) return '${(secs / size).floor()}$label ago';
  }
  return 'just now';
}
