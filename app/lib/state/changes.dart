import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'workspace.dart';

/// Where a Changes view reads from: a workspace, uncommitted or against its
/// target branch.
final class WorkspaceChangesSource {
  const WorkspaceChangesSource(this.workspaceId, this.against);
  final String workspaceId;
  final String against; // '' = uncommitted; 'target' = against the target branch

  @override
  bool operator ==(Object other) =>
      other is WorkspaceChangesSource &&
      other.workspaceId == workspaceId &&
      other.against == against;

  @override
  int get hashCode => Object.hash(workspaceId, against);
}

void refreshChanges(WidgetRef ref, WorkspaceChangesSource source) {
  ref.invalidate(
      workspaceChangedFilesProvider((source.workspaceId, source.against)));
  ref.invalidate(workspaceCommitsProvider(source.workspaceId));
}
