import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/session_repository.dart';
import '../models/enums.dart';
import '../models/session.dart';
import '../state/control.dart';
import '../state/projects_api.dart';
import '../state/sessions.dart';
import 'agent_badge.dart';
import 'details_header.dart';
import 'project_actions.dart';
import 'relative_time.dart';
import 'session_view_header.dart';
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
  final confirmed = await confirmAction(
    ax,
    title: 'Kill Session?',
    lines: [
      switch (below) {
        null => 'Child sessions: unknown. This cannot be undone!',
        0 => 'This cannot be undone!',
        1 => 'Its 1 child session is killed too. This cannot be undone!',
        _ => 'Its $below child sessions are killed too. This cannot be undone!',
      },
    ],
    action: 'Kill',
    destructive: true,
  );
  if (!confirmed) return false;
  final result = await repo.kill(s.id);
  switch (result) {
    case Ok():
      return true;
    case Error(:final error):
      ax.say('kill failed: ${actionError(error)}');
      return false;
  }
}

/// The note that replaces [s]'s actions when its node is too old for them.
String? sessionUpgradeNote(ActionContext ax, Session s) =>
    sessionOutdated(ax, s)
    ? 'Upgrade argus on ${s.nodeLabel ?? 'this node'}'
    : null;

/// The actions [s] offers: rename, and kill when it can be killed. None when
/// its node is too old for them.
List<SheetAction> sessionActions(ActionContext ax, Session s) => [
  if (!sessionOutdated(ax, s)) ...[
    SheetAction(
      label: 'Rename',
      icon: const Icon(Icons.edit_outlined),
      run: () => renameSession(ax, s),
    ),
    if (s.canKill)
      SheetAction(
        label: 'Kill',
        icon: const Icon(Icons.close),
        destructive: true,
        run: () => killSession(ax, s),
      ),
  ],
];

/// A session's details and actions: the live session header, a short table,
/// and Kill and Rename pinned at the bottom. The sidebar rows and the session
/// cards open it with a long press, and the session view with a tap on its
/// title.
Future<void> showSessionSheet(BuildContext context, Session session) {
  final ax = ActionContext.of(context);
  // The sheet follows the live session.
  Session live(WidgetRef ref) =>
      ref.watch(sessionsProvider.select((m) => m[session.id])) ?? session;
  return showDetailsSheet(
    context,
    body: (context, ref) {
      final s = live(ref);
      final task = s.summary?.task ?? '';
      return [
        SessionViewHeader(session: s),
        if (task.isNotEmpty) ...[
          const SizedBox(height: 10),
          Text(task, style: TextStyle(color: context.tokens.textSecondary)),
        ],
        const SizedBox(height: 10),
        for (final (label, value) in _fields(s))
          DetailsFieldRow(label: label, value: value),
      ];
    },
    footer: (sheet, ref) {
      final s = live(ref);
      return DetailsActionBar(
        sheet: sheet,
        actions: sessionActions(ax, s),
        note: sessionUpgradeNote(ax, s),
      );
    },
  );
}

// The sheet's fields that its header does not show.
Iterable<(String, String)> _fields(Session s) {
  final status = s.statusLabel.isNotEmpty ? s.statusLabel : s.status.name;
  final ago = relativeTime(s.summary?.lastActivity);
  final tmux = s.tmux.paneId.isEmpty
      ? ''
      : [
          s.tmux.sessionName,
          s.tmux.paneId,
          switch (s.tmux.server) {
            TmuxServerKind.default_ => 'default',
            TmuxServerKind.argus => 'argus',
            TmuxServerKind.unknown => '',
          },
        ].where((p) => p.isNotEmpty).join(' · ');
  return [
    (
      'status',
      [
        s.offline ? '$status, offline' : status,
        if (ago.isNotEmpty) ago,
      ].join(' · '),
    ),
    ('agent', agentLabel(s.agent)),
    ('dir', s.cwd ?? ''),
    ('tmux', tmux),
    ('id', s.id),
  ].where((f) => f.$2.isNotEmpty);
}
