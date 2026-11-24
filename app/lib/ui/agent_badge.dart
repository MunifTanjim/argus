import 'package:flutter/material.dart';

import '../models/agent.dart';
import 'theme.dart';

export '../models/agent.dart' show agentLabel;

Color agentColor(String agent, ArgusTokens t) =>
    readableOn(agentBrandColor(agent), t);

class AgentBadge extends StatelessWidget {
  const AgentBadge({super.key, required this.agent});

  final String agent;

  @override
  Widget build(BuildContext context) {
    final label = agentLabel(agent);
    if (label.isEmpty) return const SizedBox.shrink();
    final color = agentColor(agent, context.tokens);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        label.toUpperCase(),
        style: TextStyle(
          fontFamily: monoFamily,
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: color,
        ),
      ),
    );
  }
}
