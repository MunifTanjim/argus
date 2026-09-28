import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/project.dart';
import '../state/changes.dart';
import '../state/navigation.dart';
import 'changes_view.dart';
import 'theme.dart';

class WorkspaceChangesTab extends ConsumerWidget {
  const WorkspaceChangesTab({
    super.key,
    required this.project,
    required this.workspace,
  });

  final ProjectNode project;
  final WorkspaceNode workspace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (!project.isGit) {
      return const Center(
        child: Text(
          'Not a git repository.',
          style: TextStyle(color: AppColors.dim),
        ),
      );
    }
    final target = workspace.targetBranch;
    // A remembered "vs target" falls back when the workspace has no target.
    final against = target.isEmpty
        ? ''
        : ref.watch(changesAgainstProvider(workspace.id));
    final header = Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: SegmentedButton<String>(
        showSelectedIcon: false,
        segments: [
          const ButtonSegment(value: '', label: Text('Uncommitted')),
          ButtonSegment(
            value: 'target',
            label: Text(target.isEmpty ? 'vs target' : 'vs $target'),
            enabled: target.isNotEmpty,
          ),
        ],
        selected: {against},
        onSelectionChanged: (s) =>
            ref.read(changesAgainstProvider(workspace.id).notifier).state =
                s.first,
      ),
    );
    return ChangesView(
      source: WorkspaceChangesSource(workspace.id, against),
      header: header,
      emptyText: against.isEmpty
          ? 'No uncommitted changes.'
          : 'No changes against $target.',
      filesTitle: against.isEmpty ? null : 'Changed vs $target',
      commitsTitle: target.isEmpty ? 'Commits' : 'Commits vs $target',
    );
  }
}
