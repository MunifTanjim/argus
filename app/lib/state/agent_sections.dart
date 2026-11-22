import '../models/agent.dart';
import '../models/pagent.dart';
import 'pagents.dart';

/// A section of the Agents tab, in display order.
enum AgentSection {
  primary('Primary'),
  needsYou('Needs you'),
  waiting('Waiting'),
  working('Working'),
  unread('Unread'),
  idle('Idle'),
  unreachable('Unreachable');

  const AgentSection(this.title);
  final String title;
}

AgentSection agentSectionOf(PAgentsState s, PAgent a) {
  if (s.isPrimary(a)) return AgentSection.primary;
  return switch (a.status) {
    'unreachable' => AgentSection.unreachable,
    'needs_you' => AgentSection.needsYou,
    'waiting' => AgentSection.waiting,
    'working' => AgentSection.working,
    _ => s.unread(a.id) ? AgentSection.unread : AgentSection.idle,
  };
}

/// An agent's CLI, home node and thread count, as a row's second line.
String agentPlace(PAgentsState s, PAgent a) {
  final parts = [
    if (a.agent.isNotEmpty)
      agentLabel(a.agent).isNotEmpty ? agentLabel(a.agent) : a.agent,
    if (a.nodeLabel.isNotEmpty) a.nodeLabel,
  ];
  final ts = s.threads[a.id];
  if (ts != null) parts.add('${ts.length} ${ts.length == 1 ? 'thread' : 'threads'}');
  return parts.join(' · ');
}

bool agentMatches(PAgentsState s, PAgent a, String query) {
  final q = query.toLowerCase();
  return a.name.toLowerCase().contains(q) ||
      agentPlace(s, a).toLowerCase().contains(q);
}

typedef AgentGroup = ({AgentSection section, List<PAgent> agents});

/// The shown agents by section, by name in each; empty sections are left out.
List<AgentGroup> buildAgentSections(PAgentsState s, String query) {
  final q = query.trim();
  final by = <AgentSection, List<PAgent>>{};
  for (final a in s.agents) {
    if (a.archived || (q.isNotEmpty && !agentMatches(s, a, q))) continue;
    by.putIfAbsent(agentSectionOf(s, a), () => []).add(a);
  }
  return [
    for (final sec in AgentSection.values)
      if (by[sec] case final list? when list.isNotEmpty)
        (
          section: sec,
          agents: list
            ..sort((a, b) {
              final c = a.name.toLowerCase().compareTo(b.name.toLowerCase());
              return c != 0 ? c : a.id.compareTo(b.id);
            }),
        ),
  ];
}
