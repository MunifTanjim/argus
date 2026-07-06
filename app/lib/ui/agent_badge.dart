import 'package:flutter/material.dart';

import 'theme.dart';

/// Display label for a session's agent id. Empty for a blank id; the raw id for
/// an unknown agent. Keep in sync with internal/tui/agent.go.
String agentLabel(String agent) => agent;

Color agentColor(String agent) {
  switch (agent) {
    case 'claude':
      return const Color(0xFFfe8019);
    case 'codex':
      return const Color(0xFFb8bb26);
    case 'antigravity':
      return const Color(0xFF83a598);
    default:
      return AppColors.dim;
  }
}

class AgentBadge extends StatelessWidget {
  const AgentBadge({super.key, required this.agent});

  final String agent;

  @override
  Widget build(BuildContext context) {
    final label = agentLabel(agent);
    if (label.isEmpty) return const SizedBox.shrink();
    final color = agentColor(agent);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        label.toUpperCase(),
        style: TextStyle(
          fontFamily: 'monospace',
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: color,
        ),
      ),
    );
  }
}
