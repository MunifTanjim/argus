import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/project.dart';
import '../state/navigation.dart';
import '../state/project_tree.dart';
import '../state/projects_api.dart';
import '../state/sessions.dart';
import '../state/setup_text.dart';
import '../state/workspace.dart';
import 'branch_picker_screen.dart';
import 'git_branch_icon.dart';
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

Future<bool> _confirm(
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
              ? FilledButton.styleFrom(backgroundColor: AppColors.error)
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
  final ok = await _confirm(
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
  final ok = await _confirm(
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
}) => showDialog<void>(
  context: ax.navigator.context,
  builder: (_) => SpawnDialog(
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

Future<T?> _sheet<T>(
  BuildContext context,
  String title,
  List<(T, String, Widget, bool)> items,
) => showModalBottomSheet<T>(
  context: context,
  useRootNavigator: true,
  builder: (ctx) => SafeArea(
    child: Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        ListTile(
          title: Text(
            title,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(fontWeight: FontWeight.w600),
          ),
        ),
        for (final (value, label, icon, danger) in items)
          ListTile(
            leading: IconTheme.merge(
              data: IconThemeData(color: danger ? AppColors.error : null),
              child: icon,
            ),
            title: Text(
              label,
              style: danger ? const TextStyle(color: AppColors.error) : null,
            ),
            onTap: () => Navigator.of(ctx).pop(value),
          ),
      ],
    ),
  ),
);

Future<void> showProjectSheet(BuildContext context, ProjectNode p) async {
  final ax = ActionContext.of(context);
  final a = await _sheet(context, p.name, [
    for (final a in projectActionsFor(p))
      (a, projectActionLabel(p, a), _projectIcon(a), false),
  ]);
  if (a != null) await runProjectAction(ax, p, a);
}

Future<void> showWorkspaceSheet(
  BuildContext context,
  ProjectNode p,
  WorkspaceNode w,
) async {
  final actions = workspaceActionsFor(p, w);
  if (actions.isEmpty) return;
  final ax = ActionContext.of(context);
  final a = await _sheet(context, '${p.name} · ${w.name}', [
    for (final a in actions)
      (
        a,
        workspaceActionLabel(a),
        _workspaceIcon(a),
        a == WorkspaceAction.forceRemove,
      ),
  ]);
  if (a != null) await runWorkspaceAction(ax, p, w, a);
}
