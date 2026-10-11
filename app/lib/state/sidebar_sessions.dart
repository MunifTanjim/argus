import '../models/enums.dart';
import '../models/session.dart';

/// A section of the left sidebar's session list, in display order.
enum SidebarSection {
  needsYou('Needs you'),
  working('Working'),
  idle('Idle'),
  offline('Offline');

  const SidebarSection(this.title);
  final String title;
}

class SidebarSessionGroup {
  const SidebarSessionGroup(this.section, this.sessions);
  final SidebarSection section;
  final List<Session> sessions;
}

/// Places a session; dead sessions are not listed.
SidebarSection? sidebarSectionOf(Session s) {
  if (s.status == SessionStatus.dead) return null;
  if (s.offline) return SidebarSection.offline;
  return switch (s.status) {
    SessionStatus.awaitingInput => SidebarSection.needsYou,
    SessionStatus.working || SessionStatus.starting => SidebarSection.working,
    _ => SidebarSection.idle,
  };
}

/// The sessions by section, the most recently active first in each; empty
/// sections are left out.
List<SidebarSessionGroup> buildSidebarSections(Iterable<Session> sessions) {
  final by = <SidebarSection, List<Session>>{};
  for (final s in sessions) {
    final sec = sidebarSectionOf(s);
    if (sec != null) (by[sec] ??= []).add(s);
  }
  return [
    for (final sec in SidebarSection.values)
      if (by[sec] case final list?)
        SidebarSessionGroup(sec, list..sort(_byActivity)),
  ];
}

/// As the TUI: the session's name, its tmux session, its agent, or its id.
String sidebarTitle(Session s) => [
  s.name ?? '',
  s.tmux.sessionName,
  s.agent,
].firstWhere((l) => l.isNotEmpty, orElse: () => s.id);

final _never = DateTime.fromMillisecondsSinceEpoch(0, isUtc: true);

DateTime _activity(Session s) =>
    DateTime.tryParse(s.summary?.lastActivity ?? '') ?? _never;

int _byActivity(Session a, Session b) {
  final c = _activity(b).compareTo(_activity(a));
  return c != 0 ? c : a.id.compareTo(b.id);
}

/// Whether [q], case-insensitively, is in what the home list's filter matches
/// (name, repo, branch, node, task) or in what the row shows: its title and
/// its [place].
bool sidebarSessionMatches(Session s, String q, String place) {
  final lq = q.trim().toLowerCase();
  if (lq.isEmpty) return true;
  return [
    s.name ?? '',
    s.repo ?? '',
    s.branch ?? '',
    s.nodeLabel ?? '',
    s.summary?.task ?? '',
    sidebarTitle(s),
    place,
  ].any((f) => f.toLowerCase().contains(lq));
}
