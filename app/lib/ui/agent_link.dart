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

/// The gateway-qualified id of the agent [id] names: the id itself, or a
/// node-local id (an agent's own `argus agent` output) that one agent has.
/// Null when no agent, or more than one, matches.
String? resolveAgentLinkId(PAgentsState state, String id) {
  if (state.agents.any((a) => a.id == id)) return id;
  final local = state.agents.where((a) => a.id.endsWith(':$id')).toList();
  return local.length == 1 ? local.single.id : null;
}

/// Opens [link]: the agent's latest chat thread with the composer prefilled,
/// or the agent screen with a new chat prefilled when it has none.
void openAgentLink(BuildContext context, PAgentsState state, AgentLink link) {
  final nav = Navigator.of(context, rootNavigator: true);
  final agentId = resolveAgentLinkId(state, link.agentId);
  if (agentId == null) {
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(const SnackBar(content: Text('agent not found')));
    return;
  }
  final thread = latestChatThread(state.threads[agentId] ?? const []);
  nav.push(
    thread == null
        ? agentRoute(agentId, chatText: link.text)
        : threadRoute(
            agentId: agentId,
            threadId: thread.id,
            title: thread.title,
            initialText: link.text,
          ),
  );
}
