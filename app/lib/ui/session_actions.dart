import 'package:flutter/material.dart';

import '../core/result.dart';
import '../data/session_repository.dart';
import '../models/session.dart';
import '../state/control.dart';
import '../state/projects_api.dart';
import 'adaptive_sheet.dart';
import 'project_actions.dart';

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

/// What a session action does when chosen.
typedef SessionAction = Future<void> Function();

/// The actions [s] offers: rename, and kill when it can be killed. When its
/// node is too old for them, a note to upgrade it instead. The long-press and
/// right-click menu and the sidebar details panel both list these.
List<ActionItem<SessionAction?>> sessionActionItems(
  ActionContext ax,
  Session s,
) {
  if (sessionOutdated(ax, s)) {
    return [
      ActionItem(
        value: null,
        label: 'Upgrade argus on ${s.nodeLabel ?? 'this node'}',
        enabled: false,
        muted: true,
      ),
    ];
  }
  return [
    ActionItem(
      value: () => renameSession(ax, s),
      label: 'Rename',
      icon: const Icon(Icons.edit_outlined),
    ),
    if (s.canKill)
      ActionItem(
        value: () => killSession(ax, s),
        label: 'Kill',
        icon: const Icon(Icons.close),
        danger: true,
      ),
  ];
}

/// [sessionActionItems] as tiles for a panel opened as [sheet]; a tap closes
/// the panel and runs the action.
List<Widget> sessionActionTiles(
  BuildContext sheet,
  ActionContext ax,
  Session s,
) => [
  for (final item in sessionActionItems(ax, s))
    ListTile(
      leading: actionItemIcon(item),
      title: Text(item.label, style: actionItemLabelStyle(item)),
      onTap: switch (item.value) {
        null => null,
        final run => () {
          Navigator.of(sheet).pop();
          run();
        },
      },
    ),
];

/// Opens [s]'s [sessionActionItems] and runs the chosen one.
Future<void> showSessionActions(
  BuildContext context,
  Session s, {
  Offset? position,
}) async {
  final ax = ActionContext.of(context);
  final run = await showActionMenu<SessionAction?>(
    context,
    title: s.displayTitle,
    position: position,
    items: sessionActionItems(ax, s),
  );
  await run?.call();
}
