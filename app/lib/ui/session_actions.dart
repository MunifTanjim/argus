import 'package:flutter/material.dart';

import '../core/result.dart';
import '../data/session_repository.dart';
import '../models/session.dart';
import '../state/control.dart';
import '../state/projects_api.dart';
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

/// The actions [s] offers: rename. When its node is too old for them, a note
/// to upgrade it instead.
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
  ];
}

/// Opens [s]'s actions in a bottom sheet.
Future<void> showSessionActions(BuildContext context, Session s) {
  final ax = ActionContext.of(context);
  return showModalBottomSheet<void>(
    context: context,
    useRootNavigator: true,
    builder: (ctx) => SafeArea(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          ListTile(
            title: Text(
              s.displayTitle,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontWeight: FontWeight.w600),
            ),
          ),
          ...sessionActionTiles(ctx, ax, s),
        ],
      ),
    ),
  );
}
