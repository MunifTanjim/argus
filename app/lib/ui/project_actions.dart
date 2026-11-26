import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/project.dart';
import '../state/navigation.dart';
import '../state/project_tree.dart';
import '../state/projects.dart';
import '../state/projects_api.dart';
import '../state/sessions.dart';
import '../state/setup_text.dart';
import '../state/workspace.dart';
import 'branch_picker_screen.dart';
import 'details_header.dart';
import 'nerd_icon.dart';
import 'new_workspace_screen.dart';
import 'setup_log_screen.dart';
import 'spawn_dialog.dart';
import 'theme.dart';

/// What an action needs after the sheet, drawer, or page that started it is
/// gone: these three outlive any one screen.
class ActionContext {
  ActionContext.of(BuildContext context)
    : container = ProviderScope.containerOf(context, listen: false),
      messenger = ScaffoldMessenger.of(context),
      navigator = Navigator.of(context, rootNavigator: true);

  final ProviderContainer container;
  final ScaffoldMessengerState messenger;
  final NavigatorState navigator;

  ProjectsApi get api => container.read(projectsApiProvider);

  void say(String text) => messenger
    ..hideCurrentSnackBar()
    ..showSnackBar(SnackBar(content: Text(text)));

  Future<bool> run(String verb, Future<String> Function() call) async {
    try {
      say(await call());
      return true;
    } catch (e) {
      say('$verb failed: ${actionError(e)}');
      return false;
    }
  }
}

void _expectGone(ActionContext ax, Iterable<String> ids, {bool on = true}) {
  final n = ax.container.read(expectedGoneProvider.notifier);
  n.state = on ? {...n.state, ...ids} : n.state.difference(ids.toSet());
}

bool _refused(ActionContext ax, String name, Iterable<String> workspaceIds) {
  final text = liveGuard(
    name,
    ax.container.read(sessionsProvider).values,
    workspaceIds,
  );
  if (text != null) ax.say(text);
  return text != null;
}

/// Asks to confirm [action], with each of [lines] as a paragraph. A
/// [destructive] action's button shows in the destructive color. True once
/// confirmed.
Future<bool> confirmAction(
  ActionContext ax, {
  required String title,
  required List<String> lines,
  required String action,
  bool destructive = false,
}) async {
  final ok = await showDialog<bool>(
    context: ax.navigator.context,
    builder: (ctx) => AlertDialog(
      title: Text(title),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [for (final l in lines) Text(l)],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(false),
          child: const Text('Cancel'),
        ),
        FilledButton(
          style: destructive
              ? FilledButton.styleFrom(backgroundColor: ctx.tokens.destructive)
              : null,
          onPressed: () => Navigator.of(ctx).pop(true),
          child: Text(action),
        ),
      ],
    ),
  );
  return ok ?? false;
}

Future<void> renameProject(ActionContext ax, ProjectNode p) async {
  final name = await showDialog<String>(
    context: ax.navigator.context,
    builder: (_) => RenameDialog(current: p.name),
  );
  if (name == null) return;
  await ax.run('rename', () async {
    await ax.api.rename(p.id, name);
    return 'renamed to $name';
  });
}

Future<void> togglePinned(ActionContext ax, ProjectNode p) =>
    ax.run(p.pinned ? 'unpin' : 'pin', () async {
      await ax.api.setPinned(p.id, !p.pinned);
      return p.pinned ? 'unpinned ${p.name}' : 'pinned ${p.name}';
    });

Future<void> toggleHidden(ActionContext ax, ProjectNode p) =>
    ax.run(p.hidden ? 'unhide' : 'hide', () async {
      await ax.api.setHidden(p.id, !p.hidden);
      if (p.hidden) return 'unhid ${p.name}';
      return ax.container.read(treeViewProvider).showHidden
          ? 'hid ${p.name}'
          : 'hid ${p.name} · Show hidden in the ⋮ menu shows it';
    });

Future<void> forgetProject(ActionContext ax, ProjectNode p) async {
  if (_refused(ax, p.name, [for (final w in p.workspaces) w.id])) return;
  final ok = await confirmAction(
    ax,
    title: 'Forget project ${p.name}?',
    lines: const ['It leaves the list. Its files stay.'],
    action: 'Forget',
  );
  if (!ok) return;
  final ids = [for (final w in p.workspaces) w.id];
  _expectGone(ax, ids);
  final done = await ax.run('forget project', () async {
    await ax.api.forget(p.id);
    return 'forgot ${p.name}';
  });
  if (!done) _expectGone(ax, ids, on: false);
}

Future<void> removeWorkspace(
  ActionContext ax,
  ProjectNode p,
  WorkspaceNode w, {
  bool force = false,
}) async {
  final removing = ax.container.read(removingProvider.notifier);
  if (removing.state.contains(w.id)) {
    ax.say('already removing ${w.name}');
    return;
  }
  if (_refused(ax, w.name, [w.id])) return;
  final named = w.branch.isEmpty ? w.name : '${w.name} (${w.branch})';
  final ok = await confirmAction(
    ax,
    title: force
        ? 'Force-remove workspace $named?'
        : 'Remove workspace $named?',
    lines: [
      if (force) 'Uncommitted changes are lost.',
      if (p.teardownScript.isNotEmpty)
        'Runs teardown: ${commandLine(p.teardownScript)}',
    ],
    action: force ? 'Force remove' : 'Remove',
    destructive: force,
  );
  if (!ok) return;
  removing.state = {...removing.state, w.id};
  _expectGone(ax, [w.id]);
  final done = await ax.run('remove workspace', () async {
    final warning = await ax.api.remove(w.id, force: force);
    return warning.isEmpty
        ? 'removed ${w.name}'
        : 'removed ${w.name} · $warning';
  });
  removing.state = {...removing.state}..remove(w.id);
  if (!done) _expectGone(ax, [w.id], on: false);
}

Future<void> runSetup(ActionContext ax, WorkspaceNode w) =>
    ax.run('run setup', () async {
      await ax.api.runSetup(w.id);
      return 'running setup';
    });

Future<void> openNewWorkspace(ActionContext ax, ProjectNode p) =>
    ax.navigator.push<void>(
      MaterialPageRoute(builder: (_) => NewWorkspaceScreen(project: p)),
    );

Future<void> openSetupLog(ActionContext ax, WorkspaceNode w) =>
    ax.navigator.push<void>(
      MaterialPageRoute(builder: (_) => SetupLogScreen(workspaceId: w.id)),
    );

Future<void> spawnIn(
  ActionContext ax,
  ProjectNode p,
  WorkspaceNode w, {
  String prompt = '',
}) => ax.navigator.push(
  spawnRoute(
    target: SpawnTarget(
      nodeId: p.nodeId,
      cwd: w.dir,
      label: '${p.name} · ${w.name}',
      prompt: prompt,
    ),
  ),
);

Future<void> changeTarget(
  ActionContext ax,
  ProjectNode p,
  WorkspaceNode w,
) async {
  final branch = await pickBranch(
    ax.navigator,
    projectId: p.id,
    title: 'Target for ${w.name}',
    current: w.targetBranch,
  );
  if (branch == null) return;
  final ok = await ax.run('set target', () async {
    await ax.api.setTarget(w.id, branch);
    return 'target of ${w.name} → $branch';
  });
  if (ok) reloadChanges(ax, w.id);
}

void reloadChanges(ActionContext ax, String workspaceId) {
  for (final against in ['', 'target']) {
    ax.container.invalidate(
      workspaceChangedFilesProvider((workspaceId, against)),
    );
  }
  ax.container.invalidate(workspaceCommitsProvider(workspaceId));
}

class RenameDialog extends StatefulWidget {
  const RenameDialog({super.key, required this.current, this.title = 'Rename project'});
  final String current;
  final String title;

  @override
  State<RenameDialog> createState() => _RenameDialogState();
}

class _RenameDialogState extends State<RenameDialog> {
  late final _name = TextEditingController(text: widget.current);

  @override
  void dispose() {
    _name.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final name = _name.text.trim();
    final canSave = name.isNotEmpty && name != widget.current;
    return AlertDialog(
      title: Text(widget.title),
      content: TextField(
        controller: _name,
        autofocus: true,
        onChanged: (_) => setState(() {}),
        onSubmitted: canSave ? (_) => Navigator.of(context).pop(name) : null,
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: canSave ? () => Navigator.of(context).pop(name) : null,
          child: const Text('Save'),
        ),
      ],
    );
  }
}

enum ProjectAction { newSession, newWorkspace, rename, pin, hide, forget }

enum WorkspaceAction {
  newSession,
  changeTarget,
  rerunSetup,
  setupLog,
  remove,
  forceRemove,
}

List<ProjectAction> projectActionsFor(ProjectNode p) {
  if (p.isGone) return const [ProjectAction.forget];
  return [
    if (mainWorkspace(p) != null) ProjectAction.newSession,
    if (p.isGit) ProjectAction.newWorkspace,
    ProjectAction.rename,
    ProjectAction.pin,
    ProjectAction.hide,
    ProjectAction.forget,
  ];
}

List<WorkspaceAction> workspaceActionsFor(ProjectNode p, WorkspaceNode w) {
  final removes = w.isMain
      ? const <WorkspaceAction>[]
      : const [WorkspaceAction.remove, WorkspaceAction.forceRemove];
  if (w.isGone) return removes;
  return [
    WorkspaceAction.newSession,
    if (p.isGit) WorkspaceAction.changeTarget,
    if (p.setupScript.isNotEmpty) WorkspaceAction.rerunSetup,
    if (w.setup != null) WorkspaceAction.setupLog,
    ...removes,
  ];
}

String projectActionLabel(ProjectNode p, ProjectAction a) => switch (a) {
  ProjectAction.newSession => 'New session',
  ProjectAction.newWorkspace => 'New workspace',
  ProjectAction.rename => 'Rename',
  ProjectAction.pin => p.pinned ? 'Unpin' : 'Pin',
  ProjectAction.hide => p.hidden ? 'Unhide' : 'Hide',
  ProjectAction.forget => 'Forget',
};

String workspaceActionLabel(WorkspaceAction a) => switch (a) {
  WorkspaceAction.newSession => 'New session',
  WorkspaceAction.changeTarget => 'Change target',
  WorkspaceAction.rerunSetup => 'Rerun setup',
  WorkspaceAction.setupLog => 'Setup log',
  WorkspaceAction.remove => 'Remove',
  WorkspaceAction.forceRemove => 'Force remove',
};

Widget _projectIcon(ProjectAction a) => Icon(switch (a) {
  ProjectAction.newSession => Icons.add,
  ProjectAction.newWorkspace => Icons.call_split,
  ProjectAction.rename => Icons.edit_outlined,
  ProjectAction.pin => Icons.push_pin_outlined,
  ProjectAction.hide => Icons.visibility_off_outlined,
  ProjectAction.forget => Icons.delete_outline,
});

Widget _workspaceIcon(WorkspaceAction a) => switch (a) {
  WorkspaceAction.newSession => const Icon(Icons.add),
  WorkspaceAction.changeTarget => const GitBranchIcon(),
  WorkspaceAction.rerunSetup => const Icon(Icons.replay),
  WorkspaceAction.setupLog => const Icon(Icons.article_outlined),
  WorkspaceAction.remove => const Icon(Icons.delete_outline),
  WorkspaceAction.forceRemove => const Icon(Icons.delete_forever_outlined),
};

Future<void> runProjectAction(
  ActionContext ax,
  ProjectNode p,
  ProjectAction a,
) async {
  switch (a) {
    case ProjectAction.newSession:
      final main = mainWorkspace(p);
      if (main != null) await spawnIn(ax, p, main);
    case ProjectAction.newWorkspace:
      await openNewWorkspace(ax, p);
    case ProjectAction.rename:
      await renameProject(ax, p);
    case ProjectAction.pin:
      await togglePinned(ax, p);
    case ProjectAction.hide:
      await toggleHidden(ax, p);
    case ProjectAction.forget:
      await forgetProject(ax, p);
  }
}

Future<void> runWorkspaceAction(
  ActionContext ax,
  ProjectNode p,
  WorkspaceNode w,
  WorkspaceAction a,
) async {
  switch (a) {
    case WorkspaceAction.newSession:
      await spawnIn(ax, p, w);
    case WorkspaceAction.changeTarget:
      await changeTarget(ax, p, w);
    case WorkspaceAction.rerunSetup:
      await runSetup(ax, w);
    case WorkspaceAction.setupLog:
      await openSetupLog(ax, w);
    case WorkspaceAction.remove:
      await removeWorkspace(ax, p, w);
    case WorkspaceAction.forceRemove:
      await removeWorkspace(ax, p, w, force: true);
  }
}

/// The branch (or the short head), and the target it merges into:
/// "feat/x → main".
String workspaceAt(WorkspaceNode w) {
  final at = w.branch.isNotEmpty ? w.branch : shortHead(w.head);
  if (w.targetBranch.isEmpty) return at;
  return at.isEmpty ? '→ ${w.targetBranch}' : '$at → ${w.targetBranch}';
}

/// Where a workspace sits: "project / workspace".
String workspacePlace((ProjectNode, WorkspaceNode) hit) =>
    '${hit.$1.name} / ${hit.$2.name}';

/// The live sessions of an [Activity]: "1 blocked · 2 working", or "none".
String activityText(Activity a) {
  final idle = a.idle;
  final parts = [
    if (a.blocked > 0) '${a.blocked} blocked',
    if (a.waiting > 0) '${a.waiting} waiting',
    if (a.working > 0) '${a.working} working',
    if (idle > 0) '$idle idle',
  ];
  return parts.isEmpty ? 'none' : parts.join(' · ');
}

String _setupText(SetupRun run) {
  final state = run.state == 'failed'
      ? 'failed (exit ${run.exitCode})'
      : run.state;
  final cmd = commandLine(run.command);
  return cmd.isEmpty ? state : '$state · $cmd';
}

/// A project's details and actions: the header, a short table, Forget and
/// New session pinned at the bottom, and the other actions as tiles.
Future<void> showProjectSheet(BuildContext context, ProjectNode project) {
  final ax = ActionContext.of(context);
  // The sheet follows the live project and its sessions.
  ProjectNode live(WidgetRef ref) =>
      lookupProject(ref.watch(projectsProvider).projects, project.id) ??
      project;
  const bar = {ProjectAction.forget, ProjectAction.newSession};
  List<SheetAction> actions(ProjectNode p, {required bool pinned}) => [
    for (final a in projectActionsFor(p))
      if (bar.contains(a) == pinned)
        SheetAction(
          label: projectActionLabel(p, a),
          icon: _projectIcon(a),
          destructive: a == ProjectAction.forget,
          run: () => runProjectAction(ax, p, a),
        ),
  ];
  return showDetailsSheet(
    context,
    body: (context, ref) {
      final t = context.tokens;
      final p = live(ref);
      final ids = [for (final w in p.workspaces) w.id];
      // Rebuilds when the counts change, not on every session update.
      final act = ref.watch(
        sessionsProvider.select(
          (m) => sumActivity(workspaceActivity(m.values), ids),
        ),
      );
      final n = p.workspaces.length;
      final count = act.live == 0 ? '$n' : '$n · ${activityText(act)}';
      return [
        DetailsHeader(
          label: !p.isGit
              ? 'plain'
              : (p.defaultBranch.isEmpty ? 'git' : 'git · ${p.defaultBranch}'),
          title: p.name,
          node: p.nodeName,
          meta: [
            if (p.pinned) ('pinned', t.textDim),
            if (p.hidden) ('hidden', t.textDim),
          ],
        ),
        const SizedBox(height: 10),
        if (p.error.isNotEmpty)
          DetailsFieldRow(label: 'error', value: p.error, color: t.error),
        DetailsFieldRow(label: 'workspaces', value: count, copyable: false),
        if (p.setupScript.isNotEmpty)
          DetailsFieldRow(label: 'setup', value: p.setupScript),
        if (p.teardownScript.isNotEmpty)
          DetailsFieldRow(label: 'teardown', value: p.teardownScript),
        if (p.dir.isNotEmpty) DetailsFieldRow(label: 'dir', value: p.dir),
        const SizedBox(height: 4),
        ...sheetActionTiles(context, actions(p, pinned: false)),
      ];
    },
    footer: (sheet, ref) =>
        DetailsActionBar(sheet: sheet, actions: actions(live(ref), pinned: true)),
  );
}

/// A workspace's details and actions: the header, a short table, Remove and
/// New session pinned at the bottom, and the other actions as tiles.
Future<void> showWorkspaceSheet(
  BuildContext context,
  ProjectNode project,
  WorkspaceNode workspace,
) {
  final ax = ActionContext.of(context);
  // The sheet follows the live workspace and its sessions.
  (ProjectNode, WorkspaceNode) live(WidgetRef ref) =>
      lookupWorkspace(ref.watch(projectsProvider).projects, workspace.id) ??
      (project, workspace);
  const bar = {WorkspaceAction.remove, WorkspaceAction.newSession};
  List<SheetAction> actions(
    ProjectNode p,
    WorkspaceNode w, {
    required bool pinned,
  }) => [
    for (final a in workspaceActionsFor(p, w))
      if (bar.contains(a) == pinned)
        SheetAction(
          label: workspaceActionLabel(a),
          icon: _workspaceIcon(a),
          destructive:
              a == WorkspaceAction.remove || a == WorkspaceAction.forceRemove,
          run: () => runWorkspaceAction(ax, p, w, a),
        ),
  ];
  return showDetailsSheet(
    context,
    body: (context, ref) {
      final t = context.tokens;
      final (p, w) = live(ref);
      // Rebuilds when the counts change, not on every session update.
      final act = ref.watch(
        sessionsProvider.select(
          (m) => workspaceActivity(m.values)[w.id] ?? Activity.zero,
        ),
      );
      final setup = w.setup;
      final at = workspaceAt(w);
      return [
        DetailsHeader(
          label: p.name,
          title: w.name,
          node: p.nodeName,
          meta: [if (at.isNotEmpty) (at, t.textDim)],
        ),
        const SizedBox(height: 10),
        DetailsFieldRow(
          label: 'sessions',
          value: activityText(act),
          copyable: false,
        ),
        if (setup != null)
          DetailsFieldRow(
            label: 'setup',
            value: _setupText(setup),
            color: setup.state == 'failed' ? t.error : null,
          ),
        if (w.dir.isNotEmpty) DetailsFieldRow(label: 'dir', value: w.dir),
        if (w.head.isNotEmpty)
          DetailsFieldRow(
            label: 'head',
            value: shortHead(w.head),
            copyValue: w.head,
          ),
        const SizedBox(height: 4),
        ...sheetActionTiles(context, actions(p, w, pinned: false)),
      ];
    },
    footer: (sheet, ref) {
      final (p, w) = live(ref);
      return DetailsActionBar(
        sheet: sheet,
        actions: actions(p, w, pinned: true),
      );
    },
  );
}
