import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/project.dart';
import '../models/terminal.dart';
import '../state/control.dart';
import '../state/gateway.dart';
import '../state/projects.dart';
import '../state/projects_api.dart';
import '../state/terminals.dart';
import '../state/terminals_api.dart';
import 'details_header.dart';
import 'live_screen_screen.dart';
import 'project_actions.dart';
import 'theme.dart';

Future<void> refreshTerminals(WidgetRef ref) => ref
    .read(terminalsProvider.notifier)
    .load(ref.read(gatewayProvider)?.client);

void _open(BuildContext context, NodeTerminal t) => Navigator.of(
  context,
).push(MaterialPageRoute(builder: (_) => LiveScreenScreen(terminal: t)));

/// Creates a terminal and opens it; a null [nodeId] lets the client pick the
/// sole node.
Future<void> createTerminal(
  BuildContext context,
  WidgetRef ref,
  String? nodeId, {
  String? workspaceId,
}) async {
  try {
    final t = await ref
        .read(terminalsApiProvider)
        .create(nodeId, workspaceId: workspaceId);
    if (context.mounted) _open(context, t);
  } catch (e) {
    if (context.mounted) {
      ActionContext.of(context).say('new terminal failed: ${actionError(e)}');
    }
  }
}

class TerminalTile extends ConsumerWidget {
  const TerminalTile({super.key, required this.terminal});

  final NodeTerminal terminal;

  // Runs [action] on the terminal; a failure says so and reloads the list,
  // because a failed action can mean the terminal is gone.
  static Future<void> _run(
    ActionContext ax,
    WidgetRef ref,
    String name,
    Future<void> Function() action,
  ) async {
    try {
      await action();
    } catch (e) {
      ax.say('$name failed: ${actionError(e)}');
      await refreshTerminals(ref);
    }
  }

  static Future<void> _rename(
    ActionContext ax,
    WidgetRef ref,
    NodeTerminal t,
  ) => _run(ax, ref, 'rename', () async {
    final name = await showDialog<String>(
      context: ax.navigator.context,
      builder: (_) => RenameDialog(current: t.name, title: 'Rename terminal'),
    );
    if (name != null) await ref.read(terminalsApiProvider).rename(t.id, name);
  });

  static Future<void> _kill(ActionContext ax, WidgetRef ref, NodeTerminal t) =>
      _run(ax, ref, 'kill', () async {
        final ok = await confirmAction(
          ax,
          title: 'Kill terminal ${t.title}?',
          lines: const ['Its shell stops.'],
          action: 'Kill',
          destructive: true,
        );
        if (ok) await ref.read(terminalsApiProvider).kill(t.id);
      });

  // The terminal's details and actions: the header, a short table, and Kill
  // and Rename pinned at the bottom. The sheet follows the live terminal.
  void _showDetails(BuildContext context, WidgetRef ref) {
    final ax = ActionContext.of(context);
    NodeTerminal live(WidgetRef r) =>
        r
            .watch(terminalsProvider)
            .terminals
            .where((t) => t.id == terminal.id)
            .firstOrNull ??
        terminal;
    showDetailsSheet(
      context,
      body: (context, r) {
        final t = live(r);
        final hit = t.workspaceId.isEmpty
            ? null
            : lookupWorkspace(r.watch(projectsProvider).projects, t.workspaceId);
        return [
          DetailsHeader(
            label: hit == null ? null : workspacePlace(hit),
            title: t.title,
            node: t.nodeLabel.isEmpty ? t.nodeId : t.nodeLabel,
            meta: [if (t.attached) ('open elsewhere', context.tokens.textDim)],
          ),
          const SizedBox(height: 10),
          for (final (label, value) in [
            ('command', t.command),
            ('dir', t.cwd),
            ('id', t.id),
          ])
            if (value.isNotEmpty) DetailsFieldRow(label: label, value: value),
        ];
      },
      footer: (sheet, r) {
        final t = live(r);
        return DetailsActionBar(
          sheet: sheet,
          actions: [
            SheetAction(
              label: 'Rename',
              icon: const Icon(Icons.edit_outlined),
              run: () => _rename(ax, ref, t),
            ),
            SheetAction(
              label: 'Kill',
              icon: const Icon(Icons.close),
              destructive: true,
              run: () => _kill(ax, ref, t),
            ),
          ],
        );
      },
    );
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final t = terminal;
    // A node older than the app's minimum may not understand our requests.
    final outdated = ref.watch(
      outdatedNodeIdsProvider.select((ids) => ids.contains(t.nodeId)),
    );
    return ListTile(
      enabled: !outdated,
      titleAlignment: ListTileTitleAlignment.titleHeight,
      leading: const Icon(Icons.terminal),
      title: Text(t.title),
      subtitle: Text(t.cwd),
      trailing: t.attached
          ? const Icon(Icons.visibility_outlined, size: 18)
          : null,
      onTap: () => _open(context, t),
      onLongPress: () => _showDetails(context, ref),
    );
  }
}

/// The refreshable body of a terminal list; [children] are its rows.
class TerminalListBody extends ConsumerWidget {
  const TerminalListBody({super.key, required this.children});

  final List<Widget> children;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(terminalsProvider);
    return RefreshIndicator(
      onRefresh: () => refreshTerminals(ref),
      child: children.isEmpty
          ? ListView(
              children: [
                Padding(
                  padding: const EdgeInsets.all(32),
                  child: Center(
                    child: !state.loaded && state.error == null
                        ? const CircularProgressIndicator(
                            key: Key('terminals-loading'),
                          )
                        : Text(
                            state.error != null
                                ? 'Could not load terminals: ${state.error}'
                                : 'No terminals.',
                            style: state.error != null
                                ? TextStyle(color: context.tokens.error)
                                : null,
                          ),
                  ),
                ),
              ],
            )
          : ListView(children: children),
    );
  }
}
