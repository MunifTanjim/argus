import 'package:flutter/material.dart';

import '../core/result.dart';
import '../data/session_repository.dart';
import '../models/session.dart';
import '../state/control.dart';
import '../state/projects_api.dart';
import 'adaptive_sheet.dart';
import 'project_actions.dart';
import 'theme.dart';

/// Whether [s]'s node is too old for the app's session requests.
bool sessionOutdated(ActionContext ax, Session s) =>
    ax.container.read(outdatedNodeIdsProvider).contains(s.nodeId);

/// Renames [s] through the agent's own rename feature.
Future<void> renameSession(ActionContext ax, Session s) async {
  final name = await showDialog<String>(
    context: ax.navigator.context,
    builder: (_) => RenameDialog(current: s.name ?? '', title: 'Rename session'),
  );
  if (name == null) return;
  final result =
      await ax.container.read(sessionRepositoryProvider).rename(s.id, name);
  ax.say(switch (result) {
    Ok(value: true) => 'renamed to $name',
    Ok() => 'rename to $name sent',
    Error(:final error) => 'rename failed: ${actionError(error)}',
  });
}

/// Asks to kill [s] and kills it. True once it is killed.
Future<bool> killSession(ActionContext ax, Session s) async {
  final repo = ax.container.read(sessionRepositoryProvider);
  final below = switch (await repo.descendantCount(s.id)) {
    Ok(:final value) => value,
    Error() => null,
  };
  if (!ax.navigator.mounted) return false;
  final confirmed = await showDialog<bool>(
    context: ax.navigator.context,
    builder: (ctx) => AlertDialog(
      title: const Text('Kill Session?'),
      content: Text(switch (below) {
        null => 'Child sessions: unknown. This cannot be undone!',
        0 => 'This cannot be undone!',
        1 => 'Its 1 child session is killed too. This cannot be undone!',
        _ => 'Its $below child sessions are killed too. This cannot be undone!',
      }),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(false),
          child: const Text('Cancel'),
        ),
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(true),
          style: TextButton.styleFrom(foregroundColor: Colors.red),
          child: const Text('Kill'),
        ),
      ],
    ),
  );
  if (confirmed != true) return false;
  final result = await repo.kill(s.id);
  switch (result) {
    case Ok():
      return true;
    case Error(:final error):
      ax.say('kill failed: ${actionError(error)}');
      return false;
  }
}

/// The actions [s] offers: rename, and kill when it can be killed. When its
/// node is too old for them, a note to upgrade it instead.
List<Widget> sessionActionTiles(
  BuildContext sheet,
  ActionContext ax,
  Session s,
) {
  if (sessionOutdated(ax, s)) {
    return [
      ListTile(
        title: Text(
          'Upgrade argus on ${s.nodeLabel ?? 'this node'}',
          style: const TextStyle(color: AppColors.dim),
        ),
      ),
    ];
  }
  return [
    ListTile(
      leading: const Icon(Icons.edit_outlined),
      title: const Text('Rename'),
      onTap: () {
        Navigator.of(sheet).pop();
        renameSession(ax, s);
      },
    ),
    if (s.canKill)
      ListTile(
        leading: const Icon(Icons.close, color: AppColors.error),
        title: const Text('Kill', style: TextStyle(color: AppColors.error)),
        onTap: () {
          Navigator.of(sheet).pop();
          killSession(ax, s);
        },
      ),
  ];
}

/// Opens [s]'s actions: rename, and kill when it can be killed. When its node
/// is too old for them, a note to upgrade it instead.
Future<void> showSessionActions(
  BuildContext context,
  Session s, {
  Offset? position,
}) async {
  final ax = ActionContext.of(context);
  if (sessionOutdated(ax, s)) {
    await showActionMenu<void>(
      context,
      title: s.displayTitle,
      position: position,
      items: [
        ActionItem(
          value: null,
          label: 'Upgrade argus on ${s.nodeLabel ?? 'this node'}',
          enabled: false,
          muted: true,
        ),
      ],
    );
    return;
  }
  final action = await showActionMenu<String>(
    context,
    title: s.displayTitle,
    position: position,
    items: [
      const ActionItem(
        value: 'rename',
        label: 'Rename',
        icon: Icon(Icons.edit_outlined),
      ),
      if (s.canKill)
        const ActionItem(
          value: 'kill',
          label: 'Kill',
          icon: Icon(Icons.close),
          danger: true,
        ),
    ],
  );
  switch (action) {
    case 'rename':
      await renameSession(ax, s);
    case 'kill':
      await killSession(ax, s);
  }
}
