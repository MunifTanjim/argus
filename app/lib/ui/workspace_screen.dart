import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../e2e/aggregate.dart' show splitCompositeId;
import '../models/project.dart';
import '../state/changes.dart';
import '../state/control.dart';
import '../state/navigation.dart';
import '../state/projects.dart';
import 'project_actions.dart';
import 'session_sections_list.dart';
import 'shell_drawer.dart';
import 'spawn_dialog.dart';
import 'terminal_tile.dart';
import 'theme.dart';
import 'top_bar.dart';
import 'workspace_changes_tab.dart';
import 'workspace_files_tab.dart';
import 'workspace_sessions_tab.dart';
import 'workspace_terminals_tab.dart';

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
    final info = ref.watch(serverInfoProvider).value;
    final showTerminals =
        info?.nodes.any((n) => n.id == project.nodeId && n.terminalSupported) ??
        false;
    final selected = ref.watch(workspaceTabProvider(workspace.id));
    // A tab that disappears must not stay selected, or back resets it unseen.
    if (info != null && !showTerminals && selected == WorkspaceTab.terminals) {
      WidgetsBinding.instance.addPostFrameCallback(
        (_) => ref.read(workspaceTabProvider(workspace.id).notifier).state =
            WorkspaceTab.sessions,
      );
    }
    final tab = !showTerminals && selected == WorkspaceTab.terminals
        ? WorkspaceTab.sessions
        : selected;
    return Scaffold(
      appBar: TopBar(
        leading: shellMenuButton(context),
        automaticallyImplyLeading: false,
        title: tab == WorkspaceTab.sessions
            ? SessionSearchTitle(
                child: _WorkspaceTitle(project: project, workspace: workspace),
              )
            : _WorkspaceTitle(project: project, workspace: workspace),
        actions: [
          if (tab == WorkspaceTab.sessions) const SessionSearchButton(),
          if (tab == WorkspaceTab.changes && project.isGit)
            _RefreshChanges(workspace: workspace),
          if (workspaceActionsFor(project, workspace).isNotEmpty)
            PopupMenuButton<WorkspaceAction>(
              tooltip: 'Workspace actions',
              onSelected: (a) => runWorkspaceAction(
                ActionContext.of(context),
                project,
                workspace,
                a,
              ),
              itemBuilder: (_) => [
                for (final a in workspaceActionsFor(project, workspace))
                  PopupMenuItem(
                    value: a,
                    child: Text(
                      workspaceActionLabel(a),
                      style: a == WorkspaceAction.forceRemove
                          ? TextStyle(color: context.tokens.destructive)
                          : null,
                    ),
                  ),
              ],
            ),
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
          WorkspaceTab.terminals => WorkspaceTerminalsTab(
            workspaceId: workspace.id,
          ),
        },
      ),
      floatingActionButton: switch (tab) {
        WorkspaceTab.sessions => FloatingActionButton(
          tooltip: 'New session',
          onPressed: () => Navigator.of(context).push(
            spawnRoute(
              target: SpawnTarget(
                nodeId: project.nodeId,
                cwd: workspace.dir,
                label: '${project.name} · ${workspace.name}',
              ),
            ),
          ),
          child: const Icon(Icons.add),
        ),
        WorkspaceTab.terminals when !workspace.isGone => FloatingActionButton(
          tooltip: 'New terminal',
          onPressed: () {
            final (nodeId, localId, _) = splitCompositeId(workspace.id);
            createTerminal(context, ref, nodeId, workspaceId: localId);
          },
          child: const Icon(Icons.add),
        ),
        _ => null,
      },
      bottomNavigationBar: NavigationBar(
        selectedIndex: tab.index,
        onDestinationSelected: (i) {
          ref.read(workspaceTabProvider(workspace.id).notifier).state =
              WorkspaceTab.values[i];
          if (WorkspaceTab.values[i] == WorkspaceTab.terminals) {
            refreshTerminals(ref);
          }
        },
        destinations: [
          const NavigationDestination(
            icon: Icon(Icons.forum_outlined),
            label: 'Sessions',
          ),
          const NavigationDestination(
            icon: Icon(Icons.difference_outlined),
            label: 'Changes',
          ),
          const NavigationDestination(
            icon: Icon(Icons.folder_outlined),
            label: 'Files',
          ),
          if (showTerminals)
            const NavigationDestination(
              icon: Icon(Icons.terminal),
              label: 'Terminals',
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
      appBar: TopBar(
        title: _WorkspaceTitle(project: project, workspace: workspace),
        actions: [
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
      appBar: TopBar(
        title: _WorkspaceTitle(project: project, workspace: workspace),
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
      appBar: const TopBar(),
      body: Center(
        child: Text(
          'This workspace is no longer available.',
          style: TextStyle(color: context.tokens.textDim),
        ),
      ),
    );
  }
}

class _WorkspaceTitle extends StatelessWidget {
  const _WorkspaceTitle({required this.project, required this.workspace});

  final ProjectNode project;
  final WorkspaceNode workspace;

  // As the session view's title: the full details in a sheet, here with the
  // workspace's actions, the same sheet as a long press in the drawer.
  @override
  Widget build(BuildContext context) {
    final at = workspaceAt(workspace);
    return TopBarTitle(
      text: workspace.name,
      subtitle: [project.name, if (at.isNotEmpty) at].join(' · '),
      onTap: () => showWorkspaceSheet(context, project, workspace),
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
