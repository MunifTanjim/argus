import '../models/agent.dart';
import '../models/enums.dart';
import '../models/project.dart';
import '../models/session.dart';

class NodeRef {
  final String id;
  final String label;

  /// Whether this node can spawn sessions (tmux present). Defaults to true for
  /// session-derived nodes (they already host sessions); nodes parsed from
  /// server.info set it explicitly from the reported capability.
  final bool spawnSupported;

  /// Whether this node can run persistent terminals (tmux present). Same
  /// defaults as [spawnSupported].
  final bool terminalSupported;

  /// Whether this node can hold a wakelock (capability `host_wakelock`).
  final bool hostWakelockSupported;

  /// The node's binary version (empty when unknown, e.g. session-derived nodes).
  final String version;

  const NodeRef(
    this.id,
    this.label, {
    this.spawnSupported = true,
    this.terminalSupported = true,
    this.hostWakelockSupported = false,
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

/// The dimension that groups the home sessions list.
enum GroupBy { host, project, workspace, agent, status }

/// What leads a section's header; the UI maps it to an icon.
enum SectionIcon { needsYou, host, project, folder, branch, agent, status }

class SessionSection {
  final String title;
  final bool needsYou;
  final bool offline;
  final SectionIcon icon;

  /// The section holds sessions its dimension cannot place.
  final bool other;

  /// The cards name their node because the section does not.
  final bool showNode;
  final List<Session> sessions;

  const SessionSection({
    required this.title,
    required this.sessions,
    this.icon = SectionIcon.host,
    this.needsYou = false,
    this.offline = false,
    this.other = false,
    this.showNode = false,
  });
}

String _host(Session s) => s.nodeLabel ?? s.nodeId ?? 'local';

const _statusOrder = [
  SessionStatus.discovered,
  SessionStatus.starting,
  SessionStatus.working,
  SessionStatus.idle,
  SessionStatus.dead,
];

const _rankGroup = 1;
const _rankOther = 1 << 10;

class _Group {
  _Group(
    this.key,
    this.title,
    this.icon, {
    this.rank = _rankGroup,
    this.namesHost = false,
    String? order,
  }) : order = order ?? title;
  _Group.other(SectionIcon icon) : this('', 'other', icon, rank: _rankOther);

  final String key;
  final String title;
  final SectionIcon icon;
  final int rank;
  final bool namesHost;

  /// Sections of the same rank sort by this; it defaults to the title.
  final String order;
  final sessions = <Session>[];

  String get id => '$rank\u0000$key';

  int compareTo(_Group o) {
    if (rank != o.rank) return rank.compareTo(o.rank);
    final t = order.compareTo(o.order);
    return t != 0 ? t : key.compareTo(o.key);
  }
}

(ProjectNode, WorkspaceNode)? _workspaceOf(
    List<ProjectNode> projects, Session s) {
  final id = s.workspaceId;
  return id == null || id.isEmpty ? null : lookupWorkspace(projects, id);
}

_Group _groupOf(
    Session s, GroupBy by, List<ProjectNode> projects, bool multiNode) {
  switch (by) {
    case GroupBy.host:
      final h = _host(s);
      return _Group(h, h, SectionIcon.host, namesHost: true);
    case GroupBy.project:
      final hit = _workspaceOf(projects, s);
      return hit == null
          ? _Group.other(SectionIcon.project)
          : _Group(hit.$1.name, hit.$1.name, SectionIcon.project);
    case GroupBy.workspace:
      final hit = _workspaceOf(projects, s);
      if (hit == null) return _Group.other(SectionIcon.folder);
      final (p, w) = hit;
      final title = '${p.name} / ${w.name}';
      // A project's workspaces sort together, main first, as in the tree.
      return _Group(
        s.workspaceId!,
        multiNode ? '$title · ${_host(s)}' : title,
        // As in the drawer: a folder for the main or a plain workspace, a
        // branch for a worktree.
        w.isMain || p.kind == 'plain' ? SectionIcon.folder : SectionIcon.branch,
        namesHost: true,
        order: [
          p.name,
          w.isMain ? '0' : '1',
          w.name,
          _host(s),
        ].join('\u0000'),
      );
    case GroupBy.agent:
      return s.agent.isEmpty
          ? _Group.other(SectionIcon.agent)
          : _Group(s.agent, agentLabel(s.agent), SectionIcon.agent);
    case GroupBy.status:
      final i = _statusOrder.indexOf(s.status);
      return _Group(
        s.status.name,
        s.statusLabel.isNotEmpty ? s.statusLabel : s.status.name,
        SectionIcon.status,
        rank: _rankGroup + (i < 0 ? _statusOrder.length : i),
      );
  }
}

List<SessionSection> buildSections(
  Iterable<Session> sessions, {
  GroupBy by = GroupBy.host,
  List<ProjectNode> projects = const [],
}) {
  final all = sessions.toList();
  final multiNode = nodesFromSessions(all).isNotEmpty;
  final awaiting = all
      .where((s) => s.status == SessionStatus.awaitingInput)
      .toList()
    ..sort((a, b) {
      final h = _host(a).compareTo(_host(b));
      return h != 0 ? h : a.id.compareTo(b.id);
    });

  final groups = <String, _Group>{};
  for (final s in all) {
    if (s.status == SessionStatus.awaitingInput) continue;
    final g = _groupOf(s, by, projects, multiNode);
    (groups[g.id] ??= g).sessions.add(s);
  }
  final sorted = groups.values.toList()..sort((a, b) => a.compareTo(b));

  return [
    if (awaiting.isNotEmpty)
      SessionSection(
        title: 'Needs you',
        icon: SectionIcon.needsYou,
        needsYou: true,
        showNode: multiNode,
        sessions: awaiting,
      ),
    for (final g in sorted)
      SessionSection(
        title: g.title,
        icon: g.icon,
        other: g.rank == _rankOther,
        showNode: multiNode && !g.namesHost,
        offline: g.sessions.every((s) => s.offline),
        sessions: g.sessions..sort((a, b) => a.id.compareTo(b.id)),
      ),
  ];
}
