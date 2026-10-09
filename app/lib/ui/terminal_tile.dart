import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/terminal.dart';
import '../state/gateway.dart';
import '../state/projects_api.dart';
import '../state/terminals.dart';
import '../state/terminals_api.dart';
import 'live_screen_screen.dart';
import 'project_actions.dart';
import 'theme.dart';

Future<void> refreshTerminals(WidgetRef ref) => ref
    .read(terminalsProvider.notifier)
    .load(ref.read(gatewayProvider)?.client);

void _open(BuildContext context, NodeTerminal t) => Navigator.of(context)
    .push(MaterialPageRoute(builder: (_) => LiveScreenScreen(terminal: t)));

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

  Future<void> _actions(BuildContext context, WidgetRef ref) async {
    final t = terminal;
    final action = await showModalBottomSheet<String>(
      context: context,
      useRootNavigator: true,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              title: Text(t.title,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(fontWeight: FontWeight.w600)),
            ),
            ListTile(
              leading: const Icon(Icons.edit_outlined),
              title: const Text('Rename'),
              onTap: () => Navigator.of(ctx).pop('rename'),
            ),
            ListTile(
              leading: const Icon(Icons.close, color: AppColors.error),
              title: const Text('Kill', style: TextStyle(color: AppColors.error)),
              onTap: () => Navigator.of(ctx).pop('kill'),
            ),
          ],
        ),
      ),
    );
    if (!context.mounted || action == null) return;
    final api = ref.read(terminalsApiProvider);
    try {
      if (action == 'rename') {
        final name = await showDialog<String>(
          context: context,
          builder: (_) => RenameDialog(current: t.name, title: 'Rename terminal'),
        );
        if (name != null) await api.rename(t.id, name);
      } else {
        final ok = await showDialog<bool>(
          context: context,
          builder: (ctx) => AlertDialog(
            title: Text('Kill terminal ${t.title}?'),
            content: const Text('Its shell stops.'),
            actions: [
              TextButton(
                onPressed: () => Navigator.of(ctx).pop(false),
                child: const Text('Cancel'),
              ),
              FilledButton(
                style: FilledButton.styleFrom(backgroundColor: AppColors.error),
                onPressed: () => Navigator.of(ctx).pop(true),
                child: const Text('Kill'),
              ),
            ],
          ),
        );
        if (ok == true) await api.kill(t.id);
      }
    } catch (e) {
      if (context.mounted) ActionContext.of(context).say('$action failed: ${actionError(e)}');
      // A failed action can mean the terminal is gone, so the list reloads.
      await refreshTerminals(ref);
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final t = terminal;
    return ListTile(
      titleAlignment: ListTileTitleAlignment.titleHeight,
      leading: const Icon(Icons.terminal),
      title: Text(t.title),
      subtitle: Text(t.cwd),
      trailing: t.attached ? const Icon(Icons.visibility_outlined, size: 18) : null,
      onTap: () => _open(context, t),
      onLongPress: () => _actions(context, ref),
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
          ? ListView(children: [
              Padding(
                padding: const EdgeInsets.all(32),
                child: Center(
                  child: !state.loaded && state.error == null
                      ? const CircularProgressIndicator(key: Key('terminals-loading'))
                      : Text(
                          state.error != null
                              ? 'Could not load terminals: ${state.error}'
                              : 'No terminals.',
                          style: state.error != null
                              ? const TextStyle(color: AppColors.error)
                              : null,
                        ),
                ),
              ),
            ])
          : ListView(children: children),
    );
  }
}
