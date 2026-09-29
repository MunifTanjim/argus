import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/project.dart';
import '../state/changes.dart';
import '../state/navigation.dart';
import '../state/projects.dart';
import 'session_sections_list.dart';
import 'shell_drawer.dart';
import 'spawn_dialog.dart';
import 'theme.dart';
import 'workspace_changes_tab.dart';
import 'workspace_files_tab.dart';
import 'workspace_sessions_tab.dart';

class WorkspaceScreen extends ConsumerWidget {
  const WorkspaceScreen({
    super.key,
    required this.project,
    required this.workspace,
  });

  final ProjectNode project;
  final WorkspaceNode workspace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final tab = ref.watch(workspaceTabProvider(workspace.id));
    return Scaffold(
      appBar: AppBar(
        leading: shellMenuButton(context),
        automaticallyImplyLeading: false,
        title: _WorkspaceTitle(project: project, workspace: workspace),
        actions: [
          if (tab == WorkspaceTab.sessions) const ActiveOnlyButton(),
          if (tab == WorkspaceTab.changes && project.isGit)
            _RefreshChanges(workspace: workspace),
        ],
      ),
      body: SafeArea(
        top: false,
        child: switch (tab) {
          WorkspaceTab.sessions => WorkspaceSessionsTab(
            workspaceId: workspace.id,
          ),
          WorkspaceTab.changes => WorkspaceChangesTab(
            project: project,
            workspace: workspace,
          ),
          WorkspaceTab.files => WorkspaceFilesTab(workspace: workspace),
        },
      ),
      floatingActionButton: tab == WorkspaceTab.sessions
          ? FloatingActionButton.extended(
              onPressed: () => showSpawnDialog(
                context,
                ref,
                target: SpawnTarget(
                  nodeId: project.nodeId,
                  cwd: workspace.dir,
                  label: '${project.name} · ${workspace.name}',
                ),
              ),
              icon: const Icon(Icons.add),
              label: const Text('New session'),
            )
          : null,
      bottomNavigationBar: NavigationBar(
        selectedIndex: tab.index,
        onDestinationSelected: (i) =>
            ref.read(workspaceTabProvider(workspace.id).notifier).state =
                WorkspaceTab.values[i],
        destinations: const [
          NavigationDestination(
            icon: Icon(Icons.dashboard_outlined),
            label: 'Sessions',
          ),
          NavigationDestination(
            icon: Icon(Icons.difference_outlined),
            label: 'Changes',
          ),
          NavigationDestination(
            icon: Icon(Icons.folder_outlined),
            label: 'Files',
          ),
        ],
      ),
    );
  }
}

class WorkspaceChangesScreen extends ConsumerWidget {
  const WorkspaceChangesScreen({super.key, required this.workspaceId});

  final String workspaceId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final hit = lookupWorkspace(
      ref.watch(projectsProvider).projects,
      workspaceId,
    );
    if (hit == null) return const _WorkspaceGone();
    final (project, workspace) = hit;
    return Scaffold(
      appBar: AppBar(
        title: _WorkspaceTitle(project: project, workspace: workspace),
        actions: [
          ?shellMenuButton(context),
          if (project.isGit) _RefreshChanges(workspace: workspace),
        ],
      ),
      body: SafeArea(
        top: false,
        child: WorkspaceChangesTab(project: project, workspace: workspace),
      ),
    );
  }
}

/// Back closes it from any folder; the breadcrumb moves between folders.
class WorkspaceFilesScreen extends ConsumerWidget {
  const WorkspaceFilesScreen({super.key, required this.workspaceId});

  final String workspaceId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final hit = lookupWorkspace(
      ref.watch(projectsProvider).projects,
      workspaceId,
    );
    if (hit == null) return const _WorkspaceGone();
    final (project, workspace) = hit;
    return Scaffold(
      appBar: AppBar(
        title: _WorkspaceTitle(project: project, workspace: workspace),
        actions: [?shellMenuButton(context)],
      ),
      body: SafeArea(
        top: false,
        child: WorkspaceFilesTab(workspace: workspace),
      ),
    );
  }
}

class _WorkspaceGone extends StatelessWidget {
  const _WorkspaceGone();

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(actions: [?shellMenuButton(context)]),
      body: const Center(
        child: Text(
          'This workspace is no longer available.',
          style: TextStyle(color: AppColors.dim),
        ),
      ),
    );
  }
}

class _WorkspaceTitle extends StatelessWidget {
  const _WorkspaceTitle({required this.project, required this.workspace});

  final ProjectNode project;
  final WorkspaceNode workspace;

  String get _subtitle {
    final head = workspace.head;
    final at = workspace.branch.isNotEmpty
        ? workspace.branch
        : (head.length > 7 ? head.substring(0, 7) : head);
    final parts = [project.name, if (at.isNotEmpty) at].join(' · ');
    return workspace.targetBranch.isEmpty
        ? parts
        : '$parts → ${workspace.targetBranch}';
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(workspace.name, maxLines: 1, overflow: TextOverflow.ellipsis),
        Text(
          _subtitle,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: const TextStyle(color: AppColors.link, fontSize: 12),
        ),
      ],
    );
  }
}

class _RefreshChanges extends ConsumerWidget {
  const _RefreshChanges({required this.workspace});

  final WorkspaceNode workspace;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return IconButton(
      icon: const Icon(Icons.refresh),
      tooltip: 'Refresh',
      onPressed: () => refreshChanges(
        ref,
        WorkspaceChangesSource(
          workspace.id,
          workspace.targetBranch.isEmpty
              ? ''
              : ref.read(changesAgainstProvider(workspace.id)),
        ),
      ),
    );
  }
}
