import 'package:flutter/material.dart';

import '../models/pagent.dart';
import 'relative_time.dart';
import 'theme.dart';

/// An agent's status glyph and color, in the sessions' colors.
(String, Color) agentGlyph(String status) => switch (status) {
  'needs_you' => ('◆', AppColors.blocked),
  'waiting' => ('◆', AppColors.waiting),
  'working' => ('●', const Color(0xFFb8bb26)),
  'unreachable' => ('○', AppColors.border),
  _ => ('○', AppColors.dim),
};

/// The glyph beside a thread: its live session's status, else an empty circle.
(String, Color) threadGlyph(PAgentThread t) {
  if (t.sessionId.isEmpty) return ('○', AppColors.border);
  return switch (t.sessionStatus) {
    'working' => ('●', const Color(0xFFb8bb26)),
    'awaiting_input' => ('◆', AppColors.waiting),
    'dead' => ('×', const Color(0xFFfb4934)),
    _ => ('○', AppColors.dim),
  };
}

/// The accent dot after an item with unread activity.
class UnreadDot extends StatelessWidget {
  const UnreadDot({super.key});

  @override
  Widget build(BuildContext context) => const Padding(
    padding: EdgeInsets.only(left: 6),
    child: Icon(
      Icons.circle,
      size: 8,
      color: AppColors.accent,
      semanticLabel: 'unread',
    ),
  );
}

const agentOffline = "agent's node is offline";

/// "5m ago", or "" when [t] is unknown.
String agoText(DateTime t) =>
    t.millisecondsSinceEpoch <= 0 ? '' : relativeTimeFrom(t);

/// A byte count in whole kilobytes, or bytes under one.
String kbSize(int n) => n < 1024 ? '$n B' : '${(n + 512) ~/ 1024} KB';

/// When a routine is next due: "in 2h", "now" or "overdue 5m".
String untilText(DateTime t, [DateTime? now]) {
  final d = t.difference(now ?? DateTime.now());
  String span(Duration x) {
    if (x.inDays >= 1) return '${x.inDays}d';
    if (x.inHours >= 1) return '${x.inHours}h';
    if (x.inMinutes >= 1) return '${x.inMinutes}m';
    return '';
  }

  if (!d.isNegative) {
    final s = span(d);
    return s.isEmpty ? 'now' : 'in $s';
  }
  final s = span(-d);
  return s.isEmpty ? 'now' : 'overdue $s';
}

/// A routine's use against its budget, like "2/10 sessions · $0.40/$5.00". A
/// limit of 0 is unlimited, shown by its use alone and only once there is any.
String routineProgress(PAgentRoutine r) {
  String part(num used, num limit, String Function(num) f, String unit) {
    if (limit > 0) return '${f(used)}/${f(limit)}$unit';
    return used > 0 ? '${f(used)}$unit' : '';
  }

  String n(num v) => v.toInt().toString();
  String usd(num v) => '\$${v.toStringAsFixed(2)}';
  String k(num v) => v >= 1000 ? '${(v / 1000).toStringAsFixed(1)}k' : n(v);
  return [
    part(r.used.sessions, r.budget.sessions, n, ' sessions'),
    part(r.used.cost, r.budget.cost, usd, ''),
    part(r.used.tokens, r.budget.tokens, k, ' tokens'),
  ].where((x) => x.isNotEmpty).join(' · ');
}

/// A routine's state in words.
String routineStateText(PAgentRoutine r) => switch (r.state) {
  'paused' => r.pausedReason.isEmpty ? 'paused' : 'paused (${r.pausedReason})',
  'invalid' => r.error.isEmpty ? 'invalid' : 'invalid: ${r.error}',
  _ => r.state,
};
