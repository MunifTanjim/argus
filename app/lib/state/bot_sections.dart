import '../models/agent.dart';
import '../models/bot.dart';
import 'bots.dart';

/// A section of the Bots tab, in display order.
enum BotSection {
  primary('Primary'),
  needsYou('Needs you'),
  waiting('Waiting'),
  working('Working'),
  unread('Unread'),
  idle('Idle'),
  unreachable('Unreachable');

  const BotSection(this.title);
  final String title;
}

BotSection botSectionOf(BotsState s, Bot a) {
  if (a.primary) return BotSection.primary;
  return switch (a.status) {
    'unreachable' => BotSection.unreachable,
    'needs_you' => BotSection.needsYou,
    'waiting' => BotSection.waiting,
    'working' => BotSection.working,
    _ => s.unread(a.id) ? BotSection.unread : BotSection.idle,
  };
}

/// A bot's CLI, home node and thread count, as a row's second line.
String botPlace(BotsState s, Bot a) {
  final parts = [
    if (a.agent.isNotEmpty)
      agentLabel(a.agent).isNotEmpty ? agentLabel(a.agent) : a.agent,
    if (a.nodeLabel.isNotEmpty) a.nodeLabel,
  ];
  final ts = s.threads[a.id];
  if (ts != null) parts.add('${ts.length} ${ts.length == 1 ? 'thread' : 'threads'}');
  return parts.join(' · ');
}

bool botMatches(BotsState s, Bot a, String query) {
  final q = query.toLowerCase();
  return a.name.toLowerCase().contains(q) ||
      botPlace(s, a).toLowerCase().contains(q);
}

typedef BotGroup = ({BotSection section, List<Bot> bots});

/// The shown bots by section, by name in each; empty sections are left out.
/// Each node has its own primary, so the primaries are ordered by node first.
List<BotGroup> buildBotSections(BotsState s, String query) {
  final q = query.trim();
  final by = <BotSection, List<Bot>>{};
  for (final a in s.bots) {
    if (a.archived || (q.isNotEmpty && !botMatches(s, a, q))) continue;
    by.putIfAbsent(botSectionOf(s, a), () => []).add(a);
  }
  return [
    for (final sec in BotSection.values)
      if (by[sec] case final list? when list.isNotEmpty)
        (
          section: sec,
          bots: list
            ..sort((a, b) {
              if (sec == BotSection.primary) {
                final n = a.nodeLabel.toLowerCase().compareTo(
                  b.nodeLabel.toLowerCase(),
                );
                if (n != 0) return n;
              }
              final c = a.name.toLowerCase().compareTo(b.name.toLowerCase());
              return c != 0 ? c : a.id.compareTo(b.id);
            }),
        ),
  ];
}
