import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/enums.dart';
import '../models/project.dart';
import '../models/session.dart';

class TreeView {
  const TreeView({
    this.folded = const {},
    this.filter = '',
    this.showHidden = false,
    this.showGone = false,
  });

  final Set<String> folded; // node and project ids
  final String filter;
  final bool showHidden;
  final bool showGone;

  TreeView copyWith({
    Set<String>? folded,
    String? filter,
    bool? showHidden,
    bool? showGone,
  }) => TreeView(
    folded: folded ?? this.folded,
    filter: filter ?? this.filter,
    showHidden: showHidden ?? this.showHidden,
    showGone: showGone ?? this.showGone,
  );
}

class TreeViewNotifier extends Notifier<TreeView> {
  @override
  TreeView build() => const TreeView();

  void toggleFold(String id) {
    final next = {...state.folded};
    if (!next.remove(id)) next.add(id);
    state = state.copyWith(folded: next);
  }

  void setFilter(String text) => state = state.copyWith(filter: text);
  void toggleHidden() => state = state.copyWith(showHidden: !state.showHidden);
  void toggleGone() => state = state.copyWith(showGone: !state.showGone);
}

final treeViewProvider = NotifierProvider<TreeViewNotifier, TreeView>(
  TreeViewNotifier.new,
);

enum TreeRowKind { node, project, workspace }

enum SetupMark { none, running, failed }

class TreeRow {
  const TreeRow({
    required this.kind,
    required this.id,
    required this.label,
    this.detail,
    this.isMain = false,
    this.pinned = false,
    this.folded = false,
    this.hasKids = false,
    this.gitError = '',
    this.setup = SetupMark.none,
    this.workspaceIds = const [],
  });

  final TreeRowKind kind;
  final String id;
  final String label;
  final String? detail;
  final bool isMain;
  final bool pinned;
  final bool folded;
  final bool hasKids;
  final String gitError;
  final SetupMark setup;
  final List<String> workspaceIds;
}

SetupMark _setupMark(String? state) => switch (state) {
  'running' => SetupMark.running,
  'failed' => SetupMark.failed,
  _ => SetupMark.none,
};

String? _detail(ProjectNode p, WorkspaceNode w) {
  if (!p.isGit) return null;
  if (w.branch.isNotEmpty) return w.branch;
  if (w.head.isEmpty) return null;
  return w.head.length > 7 ? w.head.substring(0, 7) : w.head;
}

ProjectNode? _match(ProjectNode p, String q) {
  if (p.name.toLowerCase().contains(q)) return p;
  final ws = [
    for (final w in p.workspaces)
      if (w.name.toLowerCase().contains(q) ||
          w.branch.toLowerCase().contains(q))
        w,
  ];
  if (ws.isEmpty) return null;
  return p.copyWith(workspaces: ws);
}

List<TreeRow> buildTreeRows(List<ProjectNode> projects, TreeView view) {
  final q = view.filter.trim().toLowerCase();
  final byNode = <String, List<ProjectNode>>{};
  final labels = <String, String>{};
  for (var p in projects) {
    if ((p.hidden && !view.showHidden) || (p.isGone && !view.showGone)) {
      continue;
    }
    if (!view.showGone) {
      p = p.copyWith(
        workspaces: [
          for (final w in p.workspaces)
            if (!w.isGone) w,
        ],
      );
    }
    if (q.isNotEmpty) {
      final m = _match(p, q);
      if (m == null) continue;
      p = m;
    }
    (byNode[p.nodeId] ??= []).add(p);
    labels[p.nodeId] = p.nodeName;
  }
  final nodeIds = byNode.keys.toList()
    ..sort((a, b) {
      final c = labels[a]!.compareTo(labels[b]!);
      return c != 0 ? c : a.compareTo(b);
    });
  final rows = <TreeRow>[];
  for (final nid in nodeIds) {
    if (nodeIds.length > 1) {
      final folded = q.isEmpty && view.folded.contains(nid);
      rows.add(
        TreeRow(
          kind: TreeRowKind.node,
          id: nid,
          label: labels[nid]!,
          folded: folded,
          hasKids: true,
          workspaceIds: [
            for (final p in byNode[nid]!)
              for (final w in p.workspaces) w.id,
          ],
        ),
      );
      if (folded) continue;
    }
    for (final p in byNode[nid]!) {
      final folded = q.isEmpty && view.folded.contains(p.id);
      rows.add(
        TreeRow(
          kind: TreeRowKind.project,
          id: p.id,
          label: p.name,
          pinned: p.pinned,
          folded: folded,
          hasKids: p.workspaces.isNotEmpty,
          gitError: p.error,
          workspaceIds: [for (final w in p.workspaces) w.id],
        ),
      );
      if (folded) continue;
      for (final w in p.workspaces) {
        rows.add(
          TreeRow(
            kind: TreeRowKind.workspace,
            id: w.id,
            label: w.name,
            detail: _detail(p, w),
            isMain: w.isMain,
            setup: _setupMark(w.setup?.state),
            workspaceIds: [w.id],
          ),
        );
      }
    }
  }
  return rows;
}

class Activity {
  const Activity({this.live = 0, this.waiting = 0});

  final int live;
  final int waiting;

  static const zero = Activity();

  Activity operator +(Activity o) =>
      Activity(live: live + o.live, waiting: waiting + o.waiting);
}

bool _isLive(Session s) => !s.offline && s.status != SessionStatus.dead;

Activity _one(Session s) =>
    Activity(live: 1, waiting: s.status == SessionStatus.awaitingInput ? 1 : 0);

Map<String, Activity> workspaceActivity(Iterable<Session> sessions) {
  final out = <String, Activity>{};
  for (final s in sessions) {
    final ws = s.workspaceId;
    if (ws == null || ws.isEmpty || !_isLive(s)) continue;
    out[ws] = (out[ws] ?? Activity.zero) + _one(s);
  }
  return out;
}

Activity homeActivity(Iterable<Session> sessions) =>
    sessions.where(_isLive).fold(Activity.zero, (a, s) => a + _one(s));

Activity sumActivity(
  Map<String, Activity> act,
  Iterable<String> workspaceIds,
) =>
    workspaceIds.fold(Activity.zero, (a, id) => a + (act[id] ?? Activity.zero));
