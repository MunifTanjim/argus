import 'package:flutter/material.dart';

import '../models/pagent.dart';
import '../state/agent_link.dart';
import '../state/pagents.dart';
import 'agent_screen.dart';
import 'thread_screen.dart';

/// The agent's most recently active open chat thread, or null.
PAgentThread? latestChatThread(Iterable<PAgentThread> threads) {
  PAgentThread? best;
  for (final t in threads) {
    if (t.kind != 'chat' || t.status != 'active') continue;
    if (best == null || t.lastActiveAt.isAfter(best.lastActiveAt)) best = t;
  }
  return best;
}

/// Opens [link]: the agent's latest chat thread with the composer prefilled,
/// or the agent screen with a new chat prefilled when it has none.
void openAgentLink(BuildContext context, PAgentsState state, AgentLink link) {
  final nav = Navigator.of(context, rootNavigator: true);
  if (!state.agents.any((a) => a.id == link.agentId)) {
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(const SnackBar(content: Text('agent not found')));
    return;
  }
  final thread = latestChatThread(state.threads[link.agentId] ?? const []);
  nav.push(
    thread == null
        ? agentRoute(link.agentId, chatText: link.text)
        : threadRoute(
            agentId: link.agentId,
            threadId: thread.id,
            title: thread.title,
            initialText: link.text,
          ),
  );
}
