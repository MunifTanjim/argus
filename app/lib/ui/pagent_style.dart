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
