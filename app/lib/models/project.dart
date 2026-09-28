/// One git worktree (or a plain project's directory) from project.list. [id]
/// is composite (node:local) after the E2E client merges it.
class WorkspaceNode {
  const WorkspaceNode({
    required this.id,
    required this.dir,
    this.isMain = false,
    this.isGone = false,
    this.branch = '',
    this.head = '',
    this.targetBranch = '',
    this.setupState,
  });

  final String id;
  final String dir;
  final bool isMain;
  final bool isGone;
  final String branch; // empty for a detached HEAD or a plain directory
  final String head;
  final String targetBranch; // empty when the workspace has no target
  final String? setupState; // running|ok|failed; null when setup never ran

  String get name {
    final parts = dir.split('/').where((p) => p.isNotEmpty);
    return parts.isEmpty ? dir : parts.last;
  }

  factory WorkspaceNode.fromJson(Map<String, dynamic> j) {
    final setup = j['setup'];
    return WorkspaceNode(
      id: j['id'] as String? ?? '',
      dir: j['dir'] as String? ?? '',
      isMain: j['is_main'] as bool? ?? false,
      isGone: j['is_gone'] as bool? ?? false,
      branch: j['branch'] as String? ?? '',
      head: j['head'] as String? ?? '',
      targetBranch: j['target_branch'] as String? ?? '',
      setupState: setup is Map ? setup['state'] as String? : null,
    );
  }
}

class ProjectNode {
  const ProjectNode({
    required this.id,
    required this.name,
    this.kind = 'git',
    this.dir = '',
    this.isGone = false,
    this.error = '',
    this.hidden = false,
    this.pinned = false,
    this.nodeId = '',
    this.nodeLabel = '',
    this.workspaces = const [],
  });

  final String id;
  final String name;
  final String kind;
  final String dir;
  final bool isGone;
  final String error; // the git probe failure of the last list, else empty
  final bool hidden;
  final bool pinned;
  final String nodeId;
  final String nodeLabel;
  final List<WorkspaceNode> workspaces;

  bool get isGit => kind != 'plain';

  String get nodeName => nodeLabel.isNotEmpty
      ? nodeLabel
      : (nodeId.isNotEmpty ? nodeId : 'this machine');

  ProjectNode copyWith({List<WorkspaceNode>? workspaces}) => ProjectNode(
    id: id,
    name: name,
    kind: kind,
    dir: dir,
    isGone: isGone,
    error: error,
    hidden: hidden,
    pinned: pinned,
    nodeId: nodeId,
    nodeLabel: nodeLabel,
    workspaces: workspaces ?? this.workspaces,
  );

  factory ProjectNode.fromJson(Map<String, dynamic> j) {
    final ws = j['workspaces'];
    return ProjectNode(
      id: j['id'] as String? ?? '',
      name: j['name'] as String? ?? '',
      kind: j['kind'] as String? ?? 'git',
      dir: j['dir'] as String? ?? '',
      isGone: j['is_gone'] as bool? ?? false,
      error: j['error'] as String? ?? '',
      hidden: j['hidden'] as bool? ?? false,
      pinned: j['pinned'] as bool? ?? false,
      nodeId: j['node_id'] as String? ?? '',
      nodeLabel: j['node_label'] as String? ?? '',
      workspaces: [
        if (ws is List)
          for (final w in ws)
            if (w is Map<String, dynamic>) WorkspaceNode.fromJson(w),
      ],
    );
  }
}

List<ProjectNode> parseProjectList(Object? result) {
  final list = result is Map ? result['projects'] : null;
  if (list is! List) return const [];
  return [
    for (final p in list)
      if (p is Map<String, dynamic>) ProjectNode.fromJson(p),
  ];
}

(ProjectNode, WorkspaceNode)? lookupWorkspace(
  List<ProjectNode> projects,
  String workspaceId,
) {
  for (final p in projects) {
    for (final w in p.workspaces) {
      if (w.id == workspaceId) return (p, w);
    }
  }
  return null;
}
