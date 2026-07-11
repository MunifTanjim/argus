import '../models/enums.dart';
import '../models/session.dart';

class NodeRef {
  final String id;
  final String label;

  /// Whether this node can spawn sessions (tmux present). Defaults to true for
  /// session-derived nodes (they already host sessions); nodes parsed from
  /// server.info set it explicitly from the reported capability.
  final bool spawnSupported;

  /// The node's binary version (empty when unknown, e.g. session-derived nodes).
  final String version;

  const NodeRef(
    this.id,
    this.label, {
    this.spawnSupported = true,
    this.version = '',
  });
}

class AgentInfo {
  final String id;
  final String name;
  final String color;
  final bool spawnable;

  const AgentInfo({
    required this.id,
    required this.name,
    required this.color,
    required this.spawnable,
  });
}

class ResumeOutcome {
  final String sessionId;
  const ResumeOutcome({required this.sessionId});
}

List<NodeRef> nodesFromSessions(Iterable<Session> sessions) {
  final seen = <String, NodeRef>{};
  for (final s in sessions) {
    final id = s.nodeId;
    if (id == null || id.isEmpty) continue;
    if (!seen.containsKey(id)) {
      seen[id] = NodeRef(id, s.nodeLabel ?? id);
    }
  }
  final result = seen.values.toList()
    ..sort((a, b) => a.label.compareTo(b.label));
  return result;
}

class SessionSection {
  final String title;
  final bool needsYou;
  final bool offline;
  final List<Session> sessions;

  const SessionSection({
    required this.title,
    required this.sessions,
    this.needsYou = false,
    this.offline = false,
  });
}

String _host(Session s) => s.nodeLabel ?? s.nodeId ?? 'local';

List<SessionSection> buildSections(Iterable<Session> sessions) {
  final all = sessions.toList();
  final awaiting = all
      .where((s) => s.status == SessionStatus.awaitingInput)
      .toList()
    ..sort((a, b) {
      final h = _host(a).compareTo(_host(b));
      return h != 0 ? h : a.id.compareTo(b.id);
    });

  final byHost = <String, List<Session>>{};
  for (final s in all) {
    if (s.status == SessionStatus.awaitingInput) continue;
    (byHost[_host(s)] ??= []).add(s);
  }

  final hostSections = byHost.entries.map((e) {
    final list = e.value..sort((a, b) => a.id.compareTo(b.id));
    return SessionSection(
      title: e.key,
      sessions: list,
      offline: list.every((s) => s.offline),
    );
  }).toList()
    ..sort((a, b) => a.title.compareTo(b.title));

  return [
    if (awaiting.isNotEmpty)
      SessionSection(title: 'Needs you', needsYou: true, sessions: awaiting),
    ...hostSections,
  ];
}
